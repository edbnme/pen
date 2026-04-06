package tools

import "math"

type Verdict string

const (
	VerdictPass Verdict = "PASS"
	VerdictWarn Verdict = "WARN"
	VerdictFail Verdict = "FAIL"
)

type slowPageAssessment struct {
	LCPRating          Verdict
	BlockingCSSCount   int
	JSUnusedPercent    float64
	CPUHotspotDetected bool
}

type jsBloatSummary struct {
	Verdict   Verdict
	UnusedPct float64
	NextSteps []string
}

func classifyHigherIsWorse(value, warnAt, failAt float64) Verdict {
	if hasInvalidVerdictInput(value, warnAt, failAt) {
		return VerdictFail
	}

	switch {
	case value >= failAt:
		return VerdictFail
	case value >= warnAt:
		return VerdictWarn
	default:
		return VerdictPass
	}
}

func classifyLowerIsBetter(value, passAt, warnAt float64) Verdict {
	if hasInvalidVerdictInput(value, passAt, warnAt) {
		return VerdictFail
	}

	switch {
	case value <= passAt:
		return VerdictPass
	case value <= warnAt:
		return VerdictWarn
	default:
		return VerdictFail
	}
}

func hasInvalidVerdictInput(values ...float64) bool {
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return true
		}
	}

	return false
}

func slowPageNextSteps(a slowPageAssessment) []string {
	var steps []string

	if a.BlockingCSSCount > 0 {
		steps = append(steps, "Investigate render-blocking CSS and large synchronous resources first.")
	}
	if a.JSUnusedPercent >= 30 {
		steps = append(steps, "Run the JavaScript bloat workflow and check large low-usage bundles.")
	}
	if a.CPUHotspotDetected {
		steps = append(steps, "Inspect the CPU profile hotspots and long tasks before chasing network issues.")
	}
	if a.LCPRating == VerdictFail && len(steps) == 0 {
		steps = append(steps, "Re-run with network capture enabled and inspect LCP resource timing and blocking assets.")
	}
	if len(steps) == 0 {
		steps = append(steps, "No dominant bottleneck detected. Re-run after reproducing a slower interaction or page load.")
	}

	return steps
}

func summarizeJSBloatAssessment(unusedPct float64, cpuHotspot bool) jsBloatSummary {
	if hasInvalidVerdictInput(unusedPct) {
		return jsBloatSummary{
			Verdict:   VerdictFail,
			UnusedPct: unusedPct,
			NextSteps: []string{"Re-run JavaScript coverage after a full page load because the current unused percentage is invalid."},
		}
	}

	verdict := VerdictPass
	if unusedPct >= 30 {
		verdict = VerdictFail
	} else if unusedPct >= 15 || cpuHotspot {
		verdict = VerdictWarn
	}

	var steps []string
	if unusedPct >= 30 {
		steps = append(steps, "Reduce bundle size or split large low-usage scripts before tuning smaller CPU costs.")
	} else if unusedPct >= 15 {
		steps = append(steps, "Trim medium-waste bundles or split late-loading code paths before they grow into a larger JavaScript bloat problem.")
	}
	if cpuHotspot {
		steps = append(steps, "Inspect hot functions to see whether execution cost comes from framework boot, hydration, or app code.")
	}
	if len(steps) == 0 {
		steps = append(steps, "JavaScript usage looks reasonable in this sample. Re-run after reproducing the slower interaction if bundle waste is still suspected.")
	}

	return jsBloatSummary{
		Verdict:   verdict,
		UnusedPct: unusedPct,
		NextSteps: steps,
	}
}

func worstVerdict(verdicts ...Verdict) Verdict {
	worst := VerdictPass
	for _, verdict := range verdicts {
		switch verdict {
		case VerdictFail:
			return VerdictFail
		case VerdictWarn:
			worst = VerdictWarn
		}
	}

	return worst
}

func verdictFromVitalRating(rating string) Verdict {
	switch rating {
	case "Good":
		return VerdictPass
	case "Needs Improvement":
		return VerdictWarn
	case "Poor":
		return VerdictFail
	default:
		return VerdictWarn
	}
}

func appendUniqueStep(steps []string, step string) []string {
	if step == "" {
		return steps
	}

	for _, existing := range steps {
		if existing == step {
			return steps
		}
	}

	return append(steps, step)
}
