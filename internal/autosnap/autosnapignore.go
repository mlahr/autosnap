package autosnap

import (
	"bufio"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const autosnapIgnoreFileName = ".autosnapignore"

type ignoreRule struct {
	pattern   string
	negated   bool
	anchored  bool
	directory bool
}

func loadAutosnapIgnoreRules(repoRoot string) ([]ignoreRule, error) {
	file, err := os.Open(filepath.Join(repoRoot, autosnapIgnoreFileName))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer file.Close()

	var rules []ignoreRule
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		rule, ok := parseIgnoreRule(scanner.Text())
		if ok {
			rules = append(rules, rule)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return rules, nil
}

func parseIgnoreRule(line string) (ignoreRule, bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return ignoreRule{}, false
	}

	rule := ignoreRule{}
	if strings.HasPrefix(line, "!") {
		rule.negated = true
		line = strings.TrimSpace(strings.TrimPrefix(line, "!"))
	}
	if line == "" {
		return ignoreRule{}, false
	}
	if strings.HasPrefix(line, "/") {
		rule.anchored = true
		line = strings.TrimPrefix(line, "/")
	}
	if strings.HasSuffix(line, "/") {
		rule.directory = true
		line = strings.TrimSuffix(line, "/")
	}
	line = path.Clean(filepath.ToSlash(line))
	if line == "." || line == "" {
		return ignoreRule{}, false
	}
	rule.pattern = line
	return rule, true
}

func matchAutosnapIgnoreRules(rules []ignoreRule, relPath string) bool {
	relPath = path.Clean(filepath.ToSlash(relPath))
	if relPath == "." || relPath == "" {
		return false
	}

	ignored := false
	for _, rule := range rules {
		if rule.matches(relPath) {
			ignored = !rule.negated
		}
	}
	return ignored
}

func (r ignoreRule) matches(relPath string) bool {
	if r.pattern == "" {
		return false
	}
	if r.anchored {
		return matchPatternPath(r.pattern, relPath, r.directory)
	}
	if strings.Contains(r.pattern, "/") {
		return matchPatternPath(r.pattern, relPath, r.directory)
	}

	segments := strings.Split(relPath, "/")
	for _, segment := range segments {
		if matchPatternSegment(r.pattern, segment) {
			if !r.directory {
				return true
			}
			return true
		}
	}
	return false
}

func matchPatternPath(pattern, relPath string, directory bool) bool {
	if matchPathGlob(pattern, relPath) {
		return true
	}
	if directory {
		return strings.HasPrefix(relPath, pattern+"/")
	}
	return false
}

func matchPathGlob(pattern, relPath string) bool {
	matched, err := path.Match(pattern, relPath)
	if err == nil && matched {
		return true
	}
	if !strings.ContainsAny(pattern, "*?[") {
		return pattern == relPath
	}
	return false
}

func matchPatternSegment(pattern, segment string) bool {
	matched, err := path.Match(pattern, segment)
	if err == nil && matched {
		return true
	}
	if !strings.ContainsAny(pattern, "*?[") {
		return pattern == segment
	}
	return false
}
