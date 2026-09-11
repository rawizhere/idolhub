package gallery

import (
	"regexp"
	"slices"
	"strings"
)

var hashtagRe = regexp.MustCompile(`#\w+`)

// SplitList parses a comma list, treating empty and "all" as no filter.
func SplitList(v string) []string {
	if v == "" || v == "all" {
		return nil
	}
	return strings.Split(v, ",")
}

// Keep returns items matching pred, in place.
func Keep[T any](items []T, pred func(T) bool) []T {
	filtered := items[:0]
	for _, item := range items {
		if pred(item) {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

// FilterByYearMonth keeps items whose date prefix matches the selected years and months.
func FilterByYearMonth[T any](items []T, date func(T) string, years, months []string) []T {
	if len(years) == 0 && len(months) == 0 {
		return items
	}
	return Keep(items, func(item T) bool { return matchYearMonth(date(item), years, months) })
}

func matchYearMonth(date string, years, months []string) bool {
	if len(years) > 0 && (len(date) < 4 || !slices.Contains(years, date[:4])) {
		return false
	}
	if len(months) > 0 && (len(date) < 7 || !slices.Contains(months, date[5:7])) {
		return false
	}
	return true
}

// Hashtags extracts lowercased hashtags from text.
func Hashtags(text string) []string {
	matches := hashtagRe.FindAllString(text, -1)
	for i, m := range matches {
		matches[i] = strings.ToLower(m)
	}
	return matches
}

// TagSelected reports whether any selected tag appears among post tags.
func TagSelected(selected, postTags []string) bool {
	for _, st := range selected {
		if slices.Contains(postTags, strings.ToLower(strings.TrimSpace(st))) {
			return true
		}
	}
	return false
}

// Page returns one page of items and the total page count.
func Page[T any](items []T, page, size int) ([]T, int) {
	totalPages := (len(items) + size - 1) / size
	if totalPages == 0 {
		totalPages = 1
	}
	offset := (page - 1) * size
	if offset > len(items) {
		offset = len(items)
	}
	end := min(offset+size, len(items))
	return items[offset:end], totalPages
}
