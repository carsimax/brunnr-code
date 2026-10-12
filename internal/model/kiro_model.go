package model

// KiroModelAlias represents a Kiro-native model choice for role assignments.
type KiroModelAlias string

const (
	KiroModelAuto     KiroModelAlias = "auto"
	KiroModelOpus     KiroModelAlias = "opus"
	KiroModelSonnet   KiroModelAlias = "sonnet"
	KiroModelHaiku    KiroModelAlias = "haiku"
	KiroModelMiniMax  KiroModelAlias = "minimax"
	KiroModelGLM      KiroModelAlias = "glm"
	KiroModelDeepSeek KiroModelAlias = "deepseek"
	KiroModelQwen     KiroModelAlias = "qwen"
)

// Valid reports whether the alias is one of the known Kiro model options.
func (a KiroModelAlias) Valid() bool {
	switch a {
	case KiroModelAuto, KiroModelOpus, KiroModelSonnet, KiroModelHaiku,
		KiroModelMiniMax, KiroModelGLM, KiroModelDeepSeek, KiroModelQwen:
		return true
	default:
		return false
	}
}

// KiroModelPresetBalanced lets Kiro route ODD and review roles automatically.
func KiroModelPresetBalanced() map[string]KiroModelAlias {
	return map[string]KiroModelAlias{
		"orchestrator": KiroModelAuto,
		"odd-explorer": KiroModelAuto,
		"odd-worker":   KiroModelAuto,
		"odd-verify":   KiroModelAuto,
		"jd-judge-a":   KiroModelAuto,
		"jd-judge-b":   KiroModelAuto,
		"jd-fix-agent": KiroModelAuto,
		"risk":         KiroModelAuto,
		"readability":  KiroModelAuto,
		"reliability":  KiroModelAuto,
		"resilience":   KiroModelAuto,
		"refuter":      KiroModelAuto,
		"validator":    KiroModelAuto,
		"default":      KiroModelAuto,
	}
}

// KiroModelPresetPerformance prioritizes frontier Claude-family models.
func KiroModelPresetPerformance() map[string]KiroModelAlias {
	return map[string]KiroModelAlias{
		"orchestrator": KiroModelOpus,
		"odd-explorer": KiroModelSonnet,
		"odd-worker":   KiroModelSonnet,
		"odd-verify":   KiroModelOpus,
		"jd-judge-a":   KiroModelOpus,
		"jd-judge-b":   KiroModelOpus,
		"jd-fix-agent": KiroModelSonnet,
		"risk":         KiroModelOpus,
		"readability":  KiroModelSonnet,
		"reliability":  KiroModelOpus,
		"resilience":   KiroModelOpus,
		"refuter":      KiroModelOpus,
		"validator":    KiroModelOpus,
		"default":      KiroModelSonnet,
	}
}

// KiroModelPresetEconomy prioritizes Kiro's low-credit open-weight options.
func KiroModelPresetEconomy() map[string]KiroModelAlias {
	return map[string]KiroModelAlias{
		"orchestrator": KiroModelAuto,
		"odd-explorer": KiroModelQwen,
		"odd-worker":   KiroModelQwen,
		"odd-verify":   KiroModelDeepSeek,
		"jd-judge-a":   KiroModelDeepSeek,
		"jd-judge-b":   KiroModelQwen,
		"jd-fix-agent": KiroModelQwen,
		"risk":         KiroModelDeepSeek,
		"readability":  KiroModelQwen,
		"reliability":  KiroModelMiniMax,
		"resilience":   KiroModelMiniMax,
		"refuter":      KiroModelDeepSeek,
		"validator":    KiroModelDeepSeek,
		"default":      KiroModelQwen,
	}
}

// KiroModelPresetOpenWeight favors Kiro's non-Claude model families.
func KiroModelPresetOpenWeight() map[string]KiroModelAlias {
	base := KiroModelPresetEconomy()
	base["orchestrator"] = KiroModelGLM
	base["odd-verify"] = KiroModelMiniMax
	base["jd-judge-a"] = KiroModelGLM
	base["risk"] = KiroModelGLM
	return base
}
