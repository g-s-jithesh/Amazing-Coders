// Package battery models a traction pack: OCV(SoC), V = OCV − I·R, a lumped thermal model and
// the ageing ground truth. Sign convention (CLAUDE.md §4): current > 0 discharges, < 0 charges.
//
// Curves and coefficients are plausible synthetic values chosen for a realistic shape,
// not calibrated to any real cell. Every Step is O(1) and allocation-free.
package battery

import "math"

type Chemistry string

const (
	LFP Chemistry = "LFP"
	NMC Chemistry = "NMC"
)

type pt struct{ soc, v float64 }

// Approximate cell open-circuit voltage curves (V vs SoC 0..1).
var ocv = map[Chemistry][]pt{
	LFP: {{0, 2.80}, {0.05, 3.10}, {0.10, 3.20}, {0.20, 3.25}, {0.30, 3.28}, {0.50, 3.30}, {0.70, 3.32}, {0.90, 3.35}, {0.95, 3.40}, {1, 3.55}},
	NMC: {{0, 3.00}, {0.05, 3.35}, {0.10, 3.45}, {0.20, 3.55}, {0.30, 3.62}, {0.50, 3.72}, {0.70, 3.87}, {0.90, 4.05}, {1, 4.18}},
}

var nominalCellV = map[Chemistry]float64{LFP: 3.2, NMC: 3.65}

// CellOCV linearly interpolates the chemistry's OCV curve; soc is clamped to [0,1].
func CellOCV(c Chemistry, soc float64) float64 { return interp(ocv[c], soc) }

func interp(curve []pt, soc float64) float64 {
	soc = clamp(soc, 0, 1)
	for i := 1; i < len(curve); i++ {
		if soc <= curve[i].soc {
			a, b := curve[i-1], curve[i]
			return a.v + (b.v-a.v)*(soc-a.soc)/(b.soc-a.soc)
		}
	}
	return curve[len(curve)-1].v
}

const (
	rGas     = 8.314    // J/(mol·K)
	tRefK    = 298.15   // 25 °C
	eaCal    = 50_000.0 // J/mol, calendar ageing activation energy
	eaCyc    = 30_000.0 // J/mol, cycle ageing activation energy
	secPerYr = 365.25 * 86400
	coolOnC  = 32.0 // active cooling switches on above this
	coolSetC = 30.0
	irGrowth = 2.5 // R multiplier per unit of fade: 20 % fade → R × 1.5
)

// Ageing coefficients per chemistry: calendar fade per √year at 25 °C / 50 % SoC; cycle fade per EFC.
var (
	kCal = map[Chemistry]float64{LFP: 0.020, NMC: 0.030}
	kCyc = map[Chemistry]float64{LFP: 0.20 / 6000, NMC: 0.20 / 2500} // cycles to 80 % SoH at 25 °C
)

type Params struct {
	Chem          Chemistry
	CapacityKWh   float64 // nominal (SoH = 1)
	CapacityAh    float64
	Series        int
	R0Ohm         float64 // pack resistance at beginning of life, 25 °C
	ThermCapJPerK float64
	ThermResKPerW float64 // to ambient, passive
	CoolWPerK     float64 // active cooling conductance above coolOnC
	Quality       float64 // per-pack ageing multiplier (manufacturing spread), ~1, must be > 0

	curve      []pt
	kCal, kCyc float64
}

// NewParams sizes a pack from its nameplate. R0 gives ~3 % voltage sag at 1C; the thermal
// time constant to ambient is ~3 h; mass ≈ 6 kg/kWh, cp ≈ 1 kJ/(kg·K).
func NewParams(chem Chemistry, kwh, nominalV, quality float64) Params {
	ah := kwh * 1000 / nominalV
	c := kwh * 6 * 1000
	return Params{
		Chem: chem, CapacityKWh: kwh, CapacityAh: ah,
		Series:        int(math.Round(nominalV / nominalCellV[chem])),
		R0Ohm:         0.03 * nominalV / ah,
		ThermCapJPerK: c,
		ThermResKPerW: 3 * 3600 / c,
		CoolWPerK:     15 * kwh,
		Quality:       quality,
		curve:         ocv[chem],
		kCal:          kCal[chem] * quality,
		kCyc:          kCyc[chem] * quality,
	}
}

type State struct {
	SoC        float64 // 0..1
	TempC      float64 // pack average
	QCal, QCyc float64 // capacity fade fractions (ground truth)
	EFC        float64 // equivalent full cycles
	CoolingEff float64 // 1 = healthy cooling; reduced by the cooling-degradation fault
	ExtraHeatW float64 // fault-injected heat (internal short precursor etc.)
}

// SoH is usable capacity / nominal capacity.
func (s *State) SoH() float64 { return 1 - s.QCal - s.QCyc }

// Resistance grows with fade and in the cold.
func (p *Params) Resistance(s *State) float64 {
	return p.R0Ohm * (1 + irGrowth*(1-s.SoH())) * (1 + 0.02*math.Max(0, 25-s.TempC))
}

func (p *Params) OCV(s *State) float64 { return float64(p.Series) * interp(p.curve, s.SoC) }

func (p *Params) TerminalV(s *State, currentA float64) float64 {
	return p.OCV(s) - currentA*p.Resistance(s)
}

// CurrentForPower solves P = (OCV − I·R)·I for I. P > 0 discharges. Power beyond the pack's
// maximum deliverable (OCV²/4R) is clamped to that maximum.
func (p *Params) CurrentForPower(s *State, powerW float64) float64 {
	v, r := p.OCV(s), p.Resistance(s)
	d := v*v - 4*r*powerW
	if d < 0 {
		return v / (2 * r)
	}
	return (v - math.Sqrt(d)) / (2 * r)
}

func arrhenius(ea, tempC float64) float64 {
	return math.Exp(ea / rGas * (1/tRefK - 1/(tempC+273.15)))
}

// Step advances the pack by dt seconds at the given current. SoC is coulomb-counted against the
// *aged* capacity, so a measured Q = ∫I dt / ΔSoC recovers SoH (what battery-intel estimates).
func (p *Params) Step(s *State, currentA, ambientC, dt float64) {
	capAh := p.CapacityAh * s.SoH()
	s.SoC = clamp(s.SoC-currentA*dt/3600/capAh, 0, 1)

	r := p.Resistance(s)
	heat := currentA*currentA*r + s.ExtraHeatW
	cool := 0.0
	if s.TempC > coolOnC {
		cool = p.CoolWPerK * (s.TempC - coolSetC) * s.CoolingEff
	}
	s.TempC += (heat - cool + (ambientC-s.TempC)/p.ThermResKPerW) * dt / p.ThermCapJPerK

	p.age(s, currentA, dt)
}

// age is the ground-truth ageing model. Both fade terms only ever grow, so SoH is non-increasing.
//
// Calendar: q = k·√t under constant stress. With stress changing over time we keep an
// equivalent time t_eq = (q/k_eff)² and advance it, which reduces exactly to k·√t when constant.
// Cycle: dq = k_cyc · dEFC · f(C-rate) · f(T) · f(DoD).
func (p *Params) age(s *State, currentA, dt float64) {
	kEff := p.kCal * arrhenius(eaCal, s.TempC) * (0.5 + s.SoC)
	if kEff <= 0 {
		return
	}
	tEq := (s.QCal / kEff) * (s.QCal / kEff)
	s.QCal = kEff * math.Sqrt(tEq+dt/secPerYr)

	if currentA == 0 {
		return
	}
	absI := math.Abs(currentA)
	dEFC := absI * dt / 3600 / (2 * p.CapacityAh)
	s.EFC += dEFC
	cRate := absI / p.CapacityAh
	fC := 1 + 0.5*math.Max(0, cRate-1)
	fDoD := 1 + math.Abs(s.SoC-0.5) // cycling at the extremes of SoC stresses the cell more
	s.QCyc += p.kCyc * dEFC * fC * arrhenius(eaCyc, s.TempC) * fDoD
}

// PreAge sets fade in closed form for a pack that has already been in service, using average
// conditions. It is the same model integrated at constant stress.
func (p *Params) PreAge(s *State, years, avgTempC, avgSoC, efc float64) {
	if years <= 0 {
		return
	}
	s.QCal = p.kCal * arrhenius(eaCal, avgTempC) * (0.5 + avgSoC) * math.Sqrt(years)
	s.QCyc = p.kCyc * efc * arrhenius(eaCyc, avgTempC) * 1.2 // 1.2 ≈ mean C-rate/DoD factor
	s.EFC = efc
}

func clamp(x, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, x)) }
