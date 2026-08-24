package scraper

import (
	"encoding/json"
	"log/slog"
	"os"
)

// mergePostsJSON reads existing posts.json, appends unseen tweet_ids, writes back.
func mergePostsJSON(path string, newPosts []map[string]interface{}) {
	var existing []map[string]interface{}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &existing)
	}
	seen := make(map[string]bool, len(existing))
	for _, p := range existing {
		if id, ok := p["tweet_id"].(string); ok {
			seen[id] = true
		}
	}
	for _, p := range newPosts {
		if id, ok := p["tweet_id"].(string); ok && !seen[id] {
			existing = append(existing, p)
			seen[id] = true
		}
	}
	data, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		slog.Warn("failed to marshal merged posts.json", "path", path, "error", err)
		return
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		slog.Warn("failed to write merged posts.json", "path", path, "error", err)
	}
}
