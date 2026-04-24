package i18n

import (
	"os"
	"strings"
)

// StatusMode represents the UI status modes.
type StatusMode int

const (
	StatusIdle StatusMode = iota
	StatusThinking
	StatusTooling
	StatusError
	StatusDownloading
)

// T returns the translated string for a key.
// Falls back to English if the language is not supported or key not found.
func T(key string) string {
	lang := detectLang()
	return get(lang, key)
}

// detectLang returns the configured language.
func detectLang() string {
	if v := os.Getenv("V100_LANG"); v != "" {
		return normalizeLang(v)
	}
	return "en"
}

// normalizeLang normalizes a language tag (lowercases, handles "en-US" and "en_US" -> "en").
func normalizeLang(lang string) string {
	lang = strings.ToLower(lang)
	if i := strings.IndexAny(lang, "-_"); i != -1 {
		lang = lang[:i]
	}
	return lang
}

// get returns the message for a language, falling back to English.
func get(lang, key string) string {
	msg, ok := messages[lang]
	if !ok {
		msg = messages["en"]
	}
	if v, ok := msg[key]; ok {
		return v
	}
	// Fallback to English if key not in selected language
	if v, ok := messages["en"][key]; ok {
		return v
	}
	return key
}

// messages holds all translations keyed by language and message key.
var messages = map[string]map[string]string{
	"en": {
		// Status modes
		"status_idle":        "idle",
		"status_thinking":    "thinking",
		"status_tooling":     "tooling",
		"status_error":       "error",
		"status_downloading": "downloading",

		// Status line messages
		"status_ready":             "ready and waiting",
		"status_user_input":        "waiting for your input",
		"status_compressing":       "compressing context…",
		"status_waiting_interrupt": "waiting…",

		// Run summary defaults
		"run_pending":  "v100 run pending",
		"run_complete": "run complete",
		"run_error":    "run ended with error",
	},
	"fr": {
		// Status modes
		"status_idle":        "inactif",
		"status_thinking":    "réflexion",
		"status_tooling":     "outillage",
		"status_error":       "erreur",
		"status_downloading": "téléchargement",

		// Status line messages
		"status_ready":             "prêt et en attente",
		"status_user_input":        "en attente de votre entrée",
		"status_compressing":       "compression du contexte…",
		"status_waiting_interrupt": "en attente…",

		// Run summary defaults
		"run_pending":  "exécution v100 en attente",
		"run_complete": "exécution terminée",
		"run_error":    "exécution terminée avec erreur",
	},
}

// String returns the string representation of a StatusMode.
func (s StatusMode) String() string {
	switch s {
	case StatusIdle:
		return "idle"
	case StatusThinking:
		return "thinking"
	case StatusTooling:
		return "tooling"
	case StatusError:
		return "error"
	case StatusDownloading:
		return "downloading"
	default:
		return "unknown"
	}
}

// Locale returns the translated label for a StatusMode in the current language.
func (s StatusMode) Locale() string {
	if s.String() == "unknown" {
		return "unknown"
	}
	key := "status_" + s.String()
	return T(key)
}
