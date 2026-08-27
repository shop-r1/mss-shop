package locale

import (
	"sort"
	"strconv"
	"strings"
)

type preference struct {
	tag   string
	q     float64
	order int
}

func Negotiate(header string, enabled []string, defaultLocale string) string {
	available := make(map[string]string, len(enabled))
	for _, locale := range enabled {
		available[strings.ToLower(locale)] = locale
	}
	if _, ok := available[strings.ToLower(defaultLocale)]; !ok && len(enabled) > 0 {
		defaultLocale = enabled[0]
	}

	preferences := parse(header)
	for _, preferred := range preferences {
		if preferred.tag == "*" {
			return defaultLocale
		}
		canonical := canonicalCandidate(preferred.tag)
		if selected, ok := available[strings.ToLower(canonical)]; ok {
			return selected
		}
	}
	return defaultLocale
}

func parse(header string) []preference {
	parts := strings.Split(header, ",")
	result := make([]preference, 0, len(parts))
	for index, part := range parts {
		segments := strings.Split(part, ";")
		tag := strings.TrimSpace(segments[0])
		if tag == "" {
			continue
		}
		q := 1.0
		valid := true
		for _, parameter := range segments[1:] {
			name, value, found := strings.Cut(strings.TrimSpace(parameter), "=")
			if !found || !strings.EqualFold(name, "q") {
				continue
			}
			parsed, err := strconv.ParseFloat(value, 64)
			if err != nil || parsed < 0 || parsed > 1 {
				valid = false
				break
			}
			q = parsed
		}
		if valid && q > 0 {
			result = append(result, preference{tag: tag, q: q, order: index})
		}
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].q == result[j].q {
			return result[i].order < result[j].order
		}
		return result[i].q > result[j].q
	})
	return result
}

func canonicalCandidate(raw string) string {
	tag := strings.ToLower(strings.TrimSpace(raw))
	switch {
	case tag == "zh", tag == "zh-cn", tag == "zh-hans", tag == "zh-sg":
		return "zh-CN"
	case tag == "en", strings.HasPrefix(tag, "en-"):
		return "en-US"
	default:
		return raw
	}
}
