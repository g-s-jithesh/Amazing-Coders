import numpy as np
import pytest
from hypothesis import given, settings
from hypothesis import strategies as st

from app.domain.connectors import Run, TooManyVehicles, assign


def test_sticky_runs_and_no_double_booking() -> None:
    power = {
        "a": np.array([7.0, 7.0, 3.0, 0.0]),
        "b": np.array([0.0, 7.0, 7.0, 7.0]),
        "c": np.array([7.0, 0.0, 0.0, 7.0]),
    }
    runs = assign(power, 2)
    by_v: dict[str, list[Run]] = {}
    for r in runs:
        by_v.setdefault(r.vehicle_id, []).append(r)
    assert [(r.slot_start, r.slot_end, r.power_kw) for r in by_v["a"]] == [(0, 2, 7.0), (2, 3, 3.0)]
    assert by_v["a"][0].connector == by_v["a"][1].connector  # power change, same connector
    assert len({r.connector for r in by_v["b"]}) == 1


def test_too_many_vehicles_is_an_error() -> None:
    with pytest.raises(TooManyVehicles):
        assign({"a": np.ones(2), "b": np.ones(2)}, 1)


@settings(max_examples=50, deadline=None)
@given(st.lists(st.lists(st.sampled_from([0.0, 3.3, 7.2]), min_size=12, max_size=12), min_size=1, max_size=8))
def test_runs_cover_power_exactly_without_overlap(profiles: list[list[float]]) -> None:
    power = {f"v{i}": np.array(p) for i, p in enumerate(profiles)}
    n = max(1, int(max(sum(p[t] > 0 for p in profiles) for t in range(12))))
    runs = assign(power, n)
    used: set[tuple[int, int]] = set()
    rebuilt = {v: np.zeros(12) for v in power}
    for r in runs:
        assert 0 <= r.connector < n
        for t in range(r.slot_start, r.slot_end):
            assert (r.connector, t) not in used
            used.add((r.connector, t))
            rebuilt[r.vehicle_id][t] = r.power_kw
    for v in power:
        assert np.allclose(rebuilt[v], power[v])
