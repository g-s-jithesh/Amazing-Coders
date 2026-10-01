import math
import random
from dataclasses import replace

import pytest

from app.domain import soh
from app.domain.dtc import CatalogueEntry, InvalidCode, decode, defined_by
from app.domain.soh import ChargeSession, Observation, Rejection, fit_rul, kalman_update, observe

DAY = 86_400_000


def session(**kw: object) -> ChargeSession:
    base = ChargeSession(
        session_id="s",
        vin="V",
        tenant_id="t",
        start_ms=0,
        end_ms=3_600_000,
        soc_start_pct=30,
        soc_end_pct=90,
        delta_ah=-0.6 * 100,
        temp_min_c=25,
        temp_max_c=30,
        gaps=0,
        samples=360,
        rested_before=False,
    )
    return replace(base, **kw)  # type: ignore[arg-type]


def test_coulomb_counting_analytic() -> None:
    # 60 Ah into a 128.57 Ah (45 kWh / 350 V) pack moving SoC 30 → 90 % ⇒ Q = 100 Ah ⇒ SoH 77.78 %.
    o = observe(session(delta_ah=-60.0), nominal_ah=45_000 / 350)
    assert isinstance(o, Observation)
    assert o.soh_pct == pytest.approx(100 / (45_000 / 350) * 100)
    assert o.delta_soc_pct == 60


@pytest.mark.parametrize(
    ("kw", "reason"),
    [
        ({"soc_end_pct": 45}, Rejection.SMALL_DELTA_SOC),
        ({"temp_min_c": 10}, Rejection.TEMPERATURE),
        ({"temp_max_c": 40}, Rejection.TEMPERATURE),
        ({"gaps": 1}, Rejection.GAPS),
        ({"samples": 5}, Rejection.FEW_SAMPLES),
        ({"delta_ah": 5.0}, Rejection.NOT_A_CHARGE),
        ({"soc_end_pct": 20}, Rejection.NOT_A_CHARGE),
        ({"temp_min_c": float("nan")}, Rejection.NON_FINITE),
        ({"delta_ah": float("nan")}, Rejection.NON_FINITE),
        ({"soc_start_pct": float("inf")}, Rejection.NON_FINITE),
        ({"delta_ah": -500.0}, Rejection.IMPLAUSIBLE),
        ({"delta_ah": -1.0}, Rejection.IMPLAUSIBLE),
    ],
)
def test_quality_gate(kw: dict[str, object], reason: Rejection) -> None:
    assert observe(session(**kw), 100) is reason


def test_bad_capacity_and_rest_lowers_variance() -> None:
    assert observe(session(), 0) is Rejection.BAD_CAPACITY
    a, b = observe(session(), 100), observe(session(rested_before=True), 100)
    assert isinstance(a, Observation) and isinstance(b, Observation)
    assert b.variance < a.variance
    wide = observe(session(soc_end_pct=55, delta_ah=-25.0), 100)  # ΔSoC 25 vs 60: noisier
    assert isinstance(wide, Observation) and wide.variance > a.variance


def test_kalman_converges_and_ci_covers_truth() -> None:
    """Truth fades slowly; observations are noisy with their declared variance. The 95 % CI must
    contain the truth about 95 % of the time once converged, and the error must shrink."""
    rng = random.Random(7)
    covered = total = 0
    errs = []
    for _trial in range(200):
        st = None
        for day in range(120):
            truth = 95 - 0.01 * day
            sd = 1.5
            o = Observation(truth + rng.gauss(0, sd), sd**2, 60)
            st = kalman_update(st, o, day * DAY)
            if day >= 20:
                lo, hi = st.ci95()
                covered += lo <= truth <= hi
                total += 1
        assert st is not None
        errs.append(abs(st.soh_pct - truth))
    coverage = covered / total
    assert 0.90 <= coverage <= 0.995, coverage
    assert sum(errs) / len(errs) < 0.5  # vs 1.5 pp per raw observation


def test_kalman_out_of_order_adds_no_process_noise() -> None:
    st = kalman_update(None, Observation(90, 4, 50), 10 * DAY)
    older = kalman_update(st, Observation(90, 4, 50), 5 * DAY)
    assert older.as_of_ms == 10 * DAY and older.variance == pytest.approx(2.0)


def test_kalman_estimates_fade_rate() -> None:
    rng = random.Random(3)
    st = None
    for day in range(0, 200, 2):
        st = kalman_update(st, Observation(95 - 0.01 * day + rng.gauss(0, 1.0), 1.0, 60), day * DAY)
    assert st is not None and st.rate_pct_per_day == pytest.approx(-0.01, abs=0.004)


def test_rul_on_known_curve() -> None:
    a, b = 98.0, 0.3  # SoH = 98 − 0.3·√t ⇒ reaches 80 % at t = 3600 days
    pts = [(d * DAY, a - b * math.sqrt(d)) for d in range(0, 400, 10)]
    r = fit_rul(pts, now_ms=390 * DAY)
    assert r is not None
    assert r.days == pytest.approx(3600 - 390, rel=1e-6)
    assert r.fade_per_sqrt_day == pytest.approx(b)


def test_rul_needs_history_and_fade() -> None:
    assert fit_rul([(0, 95.0), (DAY, 94.9)], DAY) is None  # too few points
    assert fit_rul([(0, 95.0), (DAY, 94.9), (2 * DAY, 94.8)], 2 * DAY) is None  # span < 14 days
    flat = [(d * DAY, 95.0 + 0.001 * d) for d in range(0, 60, 5)]
    assert fit_rul(flat, 60 * DAY) is None  # no fade observed
    assert soh.EOL_SOH_PCT == 80.0


CAT = {
    "P0A7E": CatalogueEntry(
        "P0A7E",
        "powertrain",
        "CRITICAL",
        "Hybrid/EV Battery Pack Over Temperature",
        "cooling_degradation",
        "RB-THERMAL-01",
        "unverified",
    )
}


def test_dtc_decode() -> None:
    d = decode(" p0a7e ", CAT)
    assert (d.code, d.system, d.defined_by, d.catalogued, d.severity, d.runbook_id) == (
        "P0A7E",
        "powertrain",
        "SAE",
        True,
        "CRITICAL",
        "RB-THERMAL-01",
    )
    u = decode("U1234", CAT)
    assert (u.system, u.defined_by, u.catalogued, u.description) == ("network", "manufacturer", False, None)
    assert [defined_by(c) for c in ("P0000", "P1000", "P2000", "P3000")] == [
        "SAE",
        "manufacturer",
        "SAE",
        "range-dependent",
    ]
    for bad in ("X0A7E", "P4000", "P0A7", "P0G00", ""):
        with pytest.raises(InvalidCode):
            decode(bad, CAT)
