"""State-of-health estimation, physics first (services/battery-intel/CLAUDE.md "SoH method").

Pure functions and value objects; no I/O. All percentages are percentage points (0-100).

1. ``observe`` turns one charge session into a SoH observation (coulomb counting) or a rejection:
   Q_meas = |ΔAh| / ΔSoC  and  SoH_obs = Q_meas / Q_nominal.  O(1).
2. ``kalman_update`` smooths observations per pack with a local-linear-trend Kalman filter: the state is
   (SoH, fade rate in pp/day). A pure random walk lags a steadily fading pack by more than its own CI
   and under-covers; estimating the rate removes the lag. O(1) per update.
3. ``fit_rul`` fits SoH = a + b·√t and extrapolates to the end-of-life threshold. O(n).

ΔSoC uses the BMS SoC at the session ends, so its error is the BMS SoC error; the measurement
variance scales with 1/ΔSoC². A rest (≥ 30 min) before the session lowers it, because the BMS
recalibrates to OCV at rest. We deliberately do not use an OCV(SoC) table: the only one we have is the
simulator's own curve, and using it would leak simulator physics into the estimator.
"""

from __future__ import annotations

import math
from dataclasses import dataclass
from enum import StrEnum

import numpy as np

# Quality-gate thresholds.
MIN_DELTA_SOC_PCT = 20.0
MIN_TEMP_C = 15.0
MAX_TEMP_C = 35.0
MIN_SAMPLES = 10

# Measurement model (1σ): BMS SoC error per reading, and current-sensor gain error.
SOC_SIGMA_PCT = 1.0
SOC_SIGMA_RESTED_PCT = 0.5
CURRENT_GAIN_SIGMA = 0.005
# Process model (per day): small level noise, very small rate noise; prior on the fade rate.
LEVEL_VAR_PER_DAY = 0.01**2
RATE_VAR_PER_DAY = 0.0005**2
RATE_PRIOR_VAR = 0.02**2  # (pp/day)²: fleets fade ~0–0.02 pp/day
EOL_SOH_PCT = 80.0
# An observation outside this band is bad data (sensor/BMS fault), not a battery: reject, don't smooth.
PLAUSIBLE_SOH_PCT = (50.0, 120.0)


@dataclass(frozen=True, slots=True)
class ChargeSession:
    session_id: str
    vin: str
    tenant_id: str
    start_ms: int
    end_ms: int
    soc_start_pct: float
    soc_end_pct: float
    delta_ah: float  # + discharge, − charge (a charge session is negative)
    temp_min_c: float
    temp_max_c: float
    gaps: int
    samples: int
    rested_before: bool


class Rejection(StrEnum):
    NOT_A_CHARGE = "not_a_charge"
    SMALL_DELTA_SOC = "delta_soc_below_min"
    TEMPERATURE = "temperature_out_of_range"
    GAPS = "telemetry_gaps"
    FEW_SAMPLES = "too_few_samples"
    BAD_CAPACITY = "unknown_nominal_capacity"
    UNREGISTERED_VIN = "unregistered_vin"
    TENANT_MISMATCH = "tenant_mismatch"
    NON_FINITE = "non_finite_input"
    IMPLAUSIBLE = "implausible_soh"


@dataclass(frozen=True, slots=True)
class Observation:
    soh_pct: float
    variance: float  # pp²
    delta_soc_pct: float


def observe(s: ChargeSession, nominal_ah: float) -> Observation | Rejection:
    """One coulomb-counting SoH observation from a charge session, or why it was rejected."""
    if not math.isfinite(nominal_ah) or nominal_ah <= 0:
        return Rejection.BAD_CAPACITY
    if not all(map(math.isfinite, (s.soc_start_pct, s.soc_end_pct, s.delta_ah, s.temp_min_c, s.temp_max_c))):
        return Rejection.NON_FINITE  # NaN compares False everywhere and would slip through every gate below
    d_soc = s.soc_end_pct - s.soc_start_pct
    if s.delta_ah >= 0 or d_soc <= 0:
        return Rejection.NOT_A_CHARGE
    if d_soc < MIN_DELTA_SOC_PCT:
        return Rejection.SMALL_DELTA_SOC
    if s.temp_min_c < MIN_TEMP_C or s.temp_max_c > MAX_TEMP_C:
        return Rejection.TEMPERATURE
    if s.gaps > 0:
        return Rejection.GAPS
    if s.samples < MIN_SAMPLES:
        return Rejection.FEW_SAMPLES
    q_meas_ah = abs(s.delta_ah) / (d_soc / 100.0)
    soh = q_meas_ah / nominal_ah * 100.0
    if not PLAUSIBLE_SOH_PCT[0] <= soh <= PLAUSIBLE_SOH_PCT[1]:
        return Rejection.IMPLAUSIBLE
    soc_sigma = SOC_SIGMA_RESTED_PCT if s.rested_before else SOC_SIGMA_PCT
    rel_soc = math.sqrt(2.0) * soc_sigma / d_soc  # error of a difference of two readings
    variance = (soh * rel_soc) ** 2 + (soh * CURRENT_GAIN_SIGMA) ** 2
    return Observation(soh_pct=soh, variance=variance, delta_soc_pct=d_soc)


@dataclass(frozen=True, slots=True)
class KalmanState:
    soh_pct: float
    rate_pct_per_day: float  # negative = fading
    p00: float  # var(SoH)
    p01: float  # cov(SoH, rate)
    p11: float  # var(rate)
    as_of_ms: int
    n: int

    @property
    def variance(self) -> float:
        return self.p00

    def ci95(self) -> tuple[float, float]:
        half = 1.96 * math.sqrt(self.p00)
        return self.soh_pct - half, self.soh_pct + half


def kalman_update(prev: KalmanState | None, obs: Observation, as_of_ms: int) -> KalmanState:
    """Local-linear-trend Kalman step (F = [[1, dt], [0, 1]], H = [1, 0]). The first observation
    initialises the level with rate 0 and a prior variance on the rate. An observation older than the
    state is applied without prediction (no negative dt)."""
    if prev is None:
        return KalmanState(obs.soh_pct, 0.0, obs.variance, 0.0, RATE_PRIOR_VAR, as_of_ms, 1)
    dt = max(0.0, (as_of_ms - prev.as_of_ms) / 86_400_000)
    # predict
    x0 = prev.soh_pct + prev.rate_pct_per_day * dt
    x1 = prev.rate_pct_per_day
    p00 = prev.p00 + 2 * dt * prev.p01 + dt * dt * prev.p11 + LEVEL_VAR_PER_DAY * dt
    p01 = prev.p01 + dt * prev.p11
    p11 = prev.p11 + RATE_VAR_PER_DAY * dt
    # update
    s_ = p00 + obs.variance
    k0, k1 = p00 / s_, p01 / s_
    y = obs.soh_pct - x0
    return KalmanState(
        soh_pct=x0 + k0 * y,
        rate_pct_per_day=x1 + k1 * y,
        p00=(1 - k0) * p00,
        p01=(1 - k0) * p01,
        p11=p11 - k1 * p01,
        as_of_ms=max(as_of_ms, prev.as_of_ms),
        n=prev.n + 1,
    )


@dataclass(frozen=True, slots=True)
class Rul:
    days: float
    fade_per_sqrt_day: float


def fit_rul(
    points: list[tuple[int, float]],
    now_ms: int,
    eol_pct: float = EOL_SOH_PCT,
    min_points: int = 3,
    min_span_days: float = 14.0,
) -> Rul | None:
    """Fit SoH = a + b·√t (t in days since the first point) and return the days until SoH reaches
    eol_pct. None when there is too little history or no fade is observed.

    ponytail: t is measured from the first estimate, not from pack install, so a pack that is already
    aged when estimates start gets a too-steep early sqrt(t) shape (RUL biased low). Carry install_date on
    fleet.vehicle.v1 and use t since install when RUL accuracy is evaluated in ml/."""
    if len(points) < min_points:
        return None
    t0 = min(t for t, _ in points)
    t = np.array([(ms - t0) / 86_400_000 for ms, _ in points])
    if t.max() - t.min() < min_span_days:
        return None
    y = np.array([soh for _, soh in points])
    design = np.column_stack([np.ones_like(t), np.sqrt(t)])
    (a, b), *_ = np.linalg.lstsq(design, y, rcond=None)
    if b >= 0:
        return None
    t_eol = ((eol_pct - a) / b) ** 2
    now_days = (now_ms - t0) / 86_400_000
    return Rul(days=max(0.0, float(t_eol - now_days)), fade_per_sqrt_day=float(-b))
