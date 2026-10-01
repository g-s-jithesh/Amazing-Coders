"""Battery degradation cost (innovation pillar #1): a stress model priced in paise per pp of SoH lost.

Coefficients come from config (config/degradation.yaml), never from the simulator's hidden ageing
parameters (that would be leakage and would flatter the results). Per slot of length dt_h:

    calendar_pp = cal_pp_per_h · exp(cal_soc_k · (soc − 50)/50) · 2^((T − 25)/cal_temp_doubling_c) · dt_h
    cycle_pp    = cyc_pp_per_efc · (E / usable_kwh) · (1 + cyc_c_rate_k · C)
    cost        = (calendar_pp + cycle_pp) · paise_per_pp_soh

so high SoC dwell, hot packs and fast charging all cost money. Vectorised over the SoC axis: O(S).
"""

from __future__ import annotations

from dataclasses import dataclass

import numpy as np
from numpy.typing import NDArray


@dataclass(frozen=True, slots=True)
class DegradationModel:
    cal_pp_per_h: float  # calendar fade at 50 % SoC, 25 °C
    cal_soc_k: float  # SoC stress exponent
    cal_temp_doubling_c: float  # fade doubles every this many °C above 25
    cyc_pp_per_efc: float  # cycle fade per equivalent full cycle at low C-rate
    cyc_c_rate_k: float  # extra cycle stress per 1 C
    pack_cost_paise_per_kwh: float  # replacement cost
    usable_soh_range_pp: float  # SoH span from new to end of life (e.g. 100 → 80 = 20 pp)

    def paise_per_pp(self, nominal_kwh: float | NDArray[np.float64]) -> float | NDArray[np.float64]:
        return self.pack_cost_paise_per_kwh * nominal_kwh / self.usable_soh_range_pp

    def slot_cost(
        self,
        soc_pct: NDArray[np.float64],
        power_kw: float | NDArray[np.float64],
        usable_kwh: float | NDArray[np.float64],
        nominal_kwh: float | NDArray[np.float64],
        temp_c: float | NDArray[np.float64],
        dt_h: float,
    ) -> NDArray[np.float64]:
        """Paise of SoH consumed by spending dt_h at each SoC with this charging power (broadcasts)."""
        cal = (
            self.cal_pp_per_h
            * np.exp(self.cal_soc_k * (soc_pct - 50.0) / 50.0)
            * 2.0 ** ((temp_c - 25.0) / self.cal_temp_doubling_c)
            * dt_h
        )
        c_rate = power_kw / usable_kwh
        cyc = self.cyc_pp_per_efc * (power_kw * dt_h / usable_kwh) * (1.0 + self.cyc_c_rate_k * c_rate)
        return np.asarray((cal + cyc) * self.paise_per_pp(nominal_kwh), dtype=np.float64)


NO_DEGRADATION = DegradationModel(0.0, 0.0, 10.0, 0.0, 0.0, 0.0, 20.0)
