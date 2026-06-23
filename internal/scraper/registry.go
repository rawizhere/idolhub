package scraper

import "idolhub/internal/config"

type Factory func(config.Store) Scraper

var registry = map[string]Factory{}

func Register(name string, fn Factory) {
	registry[name] = fn
}

func BuildAll(cfg config.Store) map[string]Scraper {
	m := make(map[string]Scraper, len(registry))
	for name, fn := range registry {
		m[name] = fn(cfg)
	}
	return m
}
