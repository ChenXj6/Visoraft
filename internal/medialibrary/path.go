package medialibrary

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

var windowsReservedName = regexp.MustCompile(`(?i)^(con|prn|aux|nul|com[1-9]|lpt[1-9])(\..*)?$`)
var numberedScope = regexp.MustCompile(`(?i)^(part|season)[-_ ]?(\d+)$`)
var titleBrackets = []*regexp.Regexp{
	regexp.MustCompile(`【([^】]+)】`),
	regexp.MustCompile(`《([^》]+)》`),
}

func cleanSegment(value, fallback string) string {
	value = strings.Map(func(char rune) rune {
		if char < 32 || strings.ContainsRune(`<>:"/\|?*`, char) {
			return ' '
		}
		return char
	}, value)
	value = strings.Join(strings.Fields(value), " ")
	value = strings.Trim(value, " .")
	if value == "" {
		value = fallback
	}
	if windowsReservedName.MatchString(value) {
		value = "_" + value
	}
	runes := []rune(value)
	if len(runes) > 80 {
		value = strings.TrimSpace(string(runes[:80]))
	}
	return value
}

func readableLabel(value string) bool {
	for _, char := range value {
		if unicode.IsLetter(char) || unicode.IsNumber(char) {
			return true
		}
	}
	return false
}

func scopeLabel(key, name string) string {
	name = strings.TrimSpace(name)
	if readableLabel(name) {
		return name
	}
	key = strings.TrimSpace(key)
	match := numberedScope.FindStringSubmatch(key)
	if len(match) == 3 {
		unit := "部"
		if strings.EqualFold(match[1], "season") {
			unit = "季"
		}
		return "第 " + match[2] + " " + unit
	}
	if readableLabel(key) {
		return key
	}
	return ""
}

func monitorCollectionTitle(item record) string {
	if readableLabel(strings.TrimSpace(item.SeriesTitle)) {
		return strings.TrimSpace(item.SeriesTitle)
	}
	if readableLabel(strings.TrimSpace(item.MonitorName)) {
		return strings.TrimSpace(item.MonitorName)
	}
	for _, source := range []string{item.TaskTitle, item.OriginalTitle} {
		for _, pattern := range titleBrackets {
			match := pattern.FindStringSubmatch(source)
			if len(match) == 2 && readableLabel(strings.TrimSpace(match[1])) {
				return cleanSegment(match[1], "未命名监控")
			}
		}
	}
	return "未命名监控"
}

func shortID(value string) string {
	value = strings.ReplaceAll(strings.TrimSpace(value), "-", "")
	if len(value) > 8 {
		return value[:8]
	}
	if value == "" {
		return "unknown"
	}
	return value
}

func taskRelativeDirectory(item record) string {
	title := strings.TrimSpace(item.TaskTitle)
	if title == "" {
		title = item.OriginalTitle
	}
	title = cleanSegment(title, "未命名任务")
	taskFolder := title + "_" + shortID(item.TaskID)
	if item.EpisodeNumber > 0 {
		taskFolder = fmt.Sprintf("第%02d集_%s", item.EpisodeNumber, taskFolder)
	}
	if item.OriginKind != "monitor" || item.MonitorID == "" {
		return filepath.Join("独立任务", taskFolder)
	}
	collection := cleanSegment(monitorCollectionTitle(item), "未命名监控")
	parts := []string{"监控任务", collection + "_" + shortID(item.MonitorID)}
	if scope := scopeLabel(item.SeriesScopeKey, item.SeriesScopeName); scope != "" {
		parts = append(parts, cleanSegment(scope, "分组"))
	}
	parts = append(parts, taskFolder)
	return filepath.Join(parts...)
}

func safeFileName(value, kind string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		value = strings.TrimSpace(kind)
	}
	return cleanSegment(value, "文件")
}

func withAssetSuffix(name, assetID string) string {
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	return base + "_" + shortID(assetID) + ext
}

func visibleAbsolutePath(hostRoot, relative string) string {
	hostRoot = strings.TrimSpace(hostRoot)
	if hostRoot == "" {
		return relative
	}
	separator := string(filepath.Separator)
	if strings.Contains(hostRoot, `\`) || regexp.MustCompile(`^[A-Za-z]:`).MatchString(hostRoot) {
		separator = `\`
	}
	relative = strings.ReplaceAll(relative, "/", separator)
	relative = strings.ReplaceAll(relative, `\`, separator)
	return strings.TrimRight(hostRoot, `/\`) + separator + strings.TrimLeft(relative, `/\`)
}

func validHostPath(value string) bool {
	if value == "" || strings.ContainsAny(value, "\x00\r\n") || len([]rune(value)) > 500 {
		return false
	}
	if strings.HasPrefix(value, `/`) || strings.HasPrefix(value, `\\`) {
		return true
	}
	return len(value) >= 3 && unicode.IsLetter(rune(value[0])) && value[1] == ':' && (value[2] == '\\' || value[2] == '/')
}

func resolveWithinRoot(root, relative string) (string, error) {
	root = filepath.Clean(root)
	target := filepath.Clean(filepath.Join(root, relative))
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("local library path escapes configured root")
	}
	return target, nil
}
