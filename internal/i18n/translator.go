package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"sync"
)

//go:embed *.json
var translationFiles embed.FS

type Translator struct {
	mu    sync.RWMutex
	store map[string]map[string]string
}

func NewTranslator() *Translator {
	t := &Translator{
		store: make(map[string]map[string]string),
	}
	t.load("en")
	t.load("de")
	return t
}

func (t *Translator) load(locale string) {
	data, err := translationFiles.ReadFile(fmt.Sprintf("%s.json", locale))
	if err != nil {
		return
	}
	var translations map[string]string
	if err := json.Unmarshal(data, &translations); err != nil {
		return
	}
	t.mu.Lock()
	t.store[locale] = translations
	t.mu.Unlock()
}

func (t *Translator) Get(locale string) map[string]string {
	t.mu.RLock()
	translations, ok := t.store[locale]
	t.mu.RUnlock()
	if !ok {
		t.mu.RLock()
		translations = t.store["en"]
		t.mu.RUnlock()
	}
	return translations
}

func (t *Translator) T(locale, key string) string {
	t.mu.RLock()
	translations, ok := t.store[locale]
	t.mu.RUnlock()
	if !ok {
		t.mu.RLock()
		translations = t.store["en"]
		t.mu.RUnlock()
	}
	if translations != nil {
		if v, exists := translations[key]; exists {
			return v
		}
	}
	return key
}
