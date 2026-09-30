package battery

import (
	"math"
	"testing"
)

func newPack() (Params, State) {
	return NewParams(NMC, 45, 350, 1), State{SoC: 0.5, TempC: 25, CoolingEff: 1}
}

func near(a, b, relTol float64) bool {
	return math.Abs(a-b) <= relTol*math.Max(math.Abs(a), math.Abs(b))
}

func TestOCVMonotonicAndBounded(t *testing.T) {
	for _, c := range []Chemistry{LFP, NMC} {
		prev := 0.0
		for soc := -0.1; soc <= 1.1; soc += 0.01 {
			v := CellOCV(c, soc)
			if v < prev-1e-12 {
				t.Fatalf("%s OCV decreases at soc=%.2f", c, soc)
			}
			prev = v
		}
		if CellOCV(c, -1) != CellOCV(c, 0) || CellOCV(c, 2) != CellOCV(c, 1) {
			t.Fatalf("%s OCV not clamped", c)
		}
	}
}

func TestNewParamsSizing(t *testing.T) {
	p, _ := newPack()
	if p.Series != 96 || !near(p.CapacityAh, 128.57, 1e-3) {
		t.Fatalf("series=%d ah=%.2f", p.Series, p.CapacityAh)
	}
	tau := p.ThermResKPerW * p.ThermCapJPerK
	if !near(tau, 3*3600, 1e-9) {
		t.Fatalf("thermal tau = %.0f s", tau)
	}
}

func TestCurrentForPowerRoundTrip(t *testing.T) {
	p, s := newPack()
	for _, pw := range []float64{-60_000, -7_000, 0, 8_000, 50_000} {
		i := p.CurrentForPower(&s, pw)
		if got := p.TerminalV(&s, i) * i; math.Abs(got-pw) > 1e-6 {
			t.Errorf("P=%.0f: V·I = %.3f", pw, got)
		}
		if (pw > 0) != (i > 0) && pw != 0 {
			t.Errorf("P=%.0f: sign of I=%.2f wrong", pw, i)
		}
	}
	// Beyond max deliverable power → clamped at the peak current OCV/2R.
	if i := p.CurrentForPower(&s, 1e9); !near(i, p.OCV(&s)/(2*p.Resistance(&s)), 1e-12) {
		t.Errorf("overload current = %.1f", i)
	}
}

// Coulomb counting against aged capacity: ΔSoC·Q_actual = ∫I dt.
func TestCoulombConsistency(t *testing.T) {
	p, s := newPack()
	s.QCal = 0.1 // SoH 0.9
	start, ah := s.SoC, 0.0
	for i := 0; i < 1800; i++ { // 30 min at ~0.3C
		cur := p.CurrentForPower(&s, 15_000)
		ah += cur / 3600
		p.Step(&s, cur, 30, 1)
	}
	qMeas := ah / (start - s.SoC) // what battery-intel's coulomb-count SoH will compute
	if soh := qMeas / p.CapacityAh; !near(soh, s.SoH(), 1e-3) {
		t.Fatalf("coulomb SoH %.5f vs true %.5f", soh, s.SoH())
	}
}

func TestThermalRelaxesToAmbientWhenIdle(t *testing.T) {
	p, s := newPack()
	s.TempC = 40
	prevGap := s.TempC - 20
	for h := 0; h < 12; h++ {
		for i := 0; i < 3600; i++ {
			p.Step(&s, 0, 20, 1)
		}
		gap := s.TempC - 20
		if gap < 0 || gap >= prevGap {
			t.Fatalf("hour %d: gap %.3f did not shrink from %.3f", h, gap, prevGap)
		}
		prevGap = gap
	}
	// Passive cooling alone (τ = 3 h) leaves 20·e^(−4) ≈ 0.37 °C; active cooling above 32 °C only helps.
	if bound := 20 * math.Exp(-12.0/3); prevGap > bound {
		t.Fatalf("after 12 h pack is %.2f °C above ambient, want ≤ %.2f", prevGap, bound)
	}
}

func TestCoolingDegradationRunsHotter(t *testing.T) {
	run := func(eff float64) float64 {
		p, s := newPack()
		s.CoolingEff, s.TempC = eff, 35
		i := p.CurrentForPower(&s, -60_000) // DC fast charge
		for k := 0; k < 1800; k++ {
			p.Step(&s, i, 35, 1)
		}
		return s.TempC
	}
	if healthy, degraded := run(1), run(0.2); degraded <= healthy+1 {
		t.Fatalf("degraded cooling %.2f °C should be clearly hotter than healthy %.2f °C", degraded, healthy)
	}
}

// Calendar fade ∝ √t at constant stress: 4 years = 2 × 1 year; the stepped model equals PreAge.
func TestCalendarSqrtTAndMatchesPreAge(t *testing.T) {
	p, _ := newPack()
	fadeAfter := func(years float64) float64 {
		s := State{SoC: 0.5, TempC: 25, CoolingEff: 1}
		for d := 0; d < int(years*365.25); d++ {
			p.age(&s, 0, 86400)
		}
		return s.QCal
	}
	q1, q4 := fadeAfter(1), fadeAfter(4)
	if !near(q4, 2*q1, 1e-3) {
		t.Fatalf("q(4y)=%.5f, want 2×q(1y)=%.5f", q4, 2*q1)
	}
	if !near(q1, 0.030, 1e-3) { // NMC k_cal at 25 °C, SoC 0.5
		t.Fatalf("q(1y)=%.5f, want 0.030", q1)
	}
	var s State
	p.PreAge(&s, 4, 25, 0.5, 0)
	if !near(s.QCal, q4, 1e-3) {
		t.Fatalf("PreAge=%.5f vs stepped=%.5f", s.QCal, q4)
	}
}

func TestAgeingAcceleratesWithTempAndSoC(t *testing.T) {
	p, _ := newPack()
	fade := func(tempC, soc float64) float64 {
		s := State{SoC: soc, TempC: tempC}
		for d := 0; d < 365; d++ {
			p.age(&s, 0, 86400)
		}
		return s.QCal
	}
	base := fade(25, 0.5)
	if hot := fade(40, 0.5); hot < 2*base {
		t.Errorf("40 °C fade %.4f not ≥ 2× 25 °C fade %.4f", hot, base)
	}
	if high := fade(25, 0.95); high <= base*1.3 {
		t.Errorf("95%% SoC fade %.4f not clearly above 50%% fade %.4f", high, base)
	}
}

func TestCycleFadeAndIRGrowth(t *testing.T) {
	p, s := newPack()
	r0 := p.Resistance(&s)
	prevSoH := s.SoH()
	i1C := p.CapacityAh
	for cyc := 0; cyc < 200; cyc++ {
		for s.SoC > 0.1 {
			p.Step(&s, i1C, 25, 10)
		}
		for s.SoC < 0.9 {
			p.Step(&s, -i1C, 25, 10)
		}
		if soh := s.SoH(); soh > prevSoH {
			t.Fatalf("SoH increased at cycle %d", cyc)
		}
		prevSoH = s.SoH()
	}
	if s.EFC < 150 || s.QCyc <= 0 {
		t.Fatalf("EFC=%.1f qcyc=%.5f", s.EFC, s.QCyc)
	}
	s.TempC = 25
	if p.Resistance(&s) <= r0 {
		t.Fatalf("IR did not grow with fade")
	}
	if p.Resistance(&State{TempC: 0}) <= p.Resistance(&State{TempC: 25}) {
		t.Fatalf("IR should be higher in the cold")
	}
}

func BenchmarkStep(b *testing.B) {
	p, s := newPack()
	for i := 0; i < b.N; i++ {
		p.Step(&s, 20, 30, 1)
		if s.SoC < 0.1 {
			s.SoC = 0.9
		}
	}
}
