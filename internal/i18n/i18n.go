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
	if v := os.Getenv("LANG"); v != "" {
		return normalizeLang(v)
	}
	return "en"
}

// normalizeLang normalizes a language tag (lowercases, handles "en-US", "en_US", and "en_US.UTF-8" -> "en").
func normalizeLang(lang string) string {
	lang = strings.ToLower(lang)
	if i := strings.IndexByte(lang, '.'); i != -1 {
		lang = lang[:i]
	}
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
		"status_scanning_context":  "scanning context and constraints",
		"status_planning":          "planning a clean approach",
		"status_looking_code":      "looking at code",
		"status_searching_repo":    "searching repo",
		"status_running_tools":     "running tools for signal",
		"status_ready_next":        "ready for your next move",
		"status_response_done":     "response delivered",
		"status_standing_by":       "standing by",
		"status_executing_tool":    "executing tool call",
		"status_collecting":        "collecting evidence",
		"status_digging_files":     "digging through files",
		"status_stitching":         "stitching tool outputs together",
		"status_cross_checking":    "cross-checking findings",
		"status_digesting":         "digesting information",
		"status_run_error":         "hit an error; check transcript",
		"status_stalled":           "STALLED",

		// TUI chrome
		"ui_initializing_terminal": "Initializing terminal size...",
		"ui_header_hint_wide":      "  Tab:focus  Shift+Tab:back  Ctrl+PgUp/PgDn:half  Shift+Arrows:resize  Ctrl+T:trace  Ctrl+S:status  Ctrl+M:inspector  Ctrl+D:detail  Ctrl+A:copy all  Ctrl+C:quit",
		"ui_header_hint_medium":    "  Tab:focus  Shift+Tab:back  Ctrl+PgUp/PgDn:half  Ctrl+T:trace  Ctrl+S:status  Ctrl+M:inspector  Ctrl+D:detail  Ctrl+A:copy all  Ctrl+C:quit",
		"ui_header_hint_narrow":    "  Tab:focus  Ctrl+PgUp/PgDn:half  Ctrl+T:trace  Ctrl+M:inspector  Ctrl+D:detail  Ctrl+A:copy all  Ctrl+C:quit",
		"ui_header_hint_tiny":      "  Tab:focus  Ctrl+PgUp/PgDn:half  Ctrl+M:inspect  Ctrl+D:detail  Ctrl+A:copy  Ctrl+C:quit",
		"ui_status":                "status",
		"ui_trace":                 "trace",
		"ui_radio":                 "radio",
		"ui_feed":                  "feed",
		"ui_now":                   "now",
		"ui_sub_agents":            "sub-agents: active=%d done=%d failed=%d",
		"ui_current_agent":         "current: %s  %s",
		"ui_last_agent":            "last: %s",
		"ui_radio_idle":            "idle",
		"ui_radio_playing":         "playing",
		"ui_radio_controls":        "%s  %s  vol=%d%%  Ctrl+R play/stop  [/] volume  N/P station",
		"ui_custom":                "Custom",
		"ui_dangerous_tool_call":   "DANGEROUS TOOL CALL",
		"ui_tool":                  "Tool",
		"ui_args":                  "Args",
		"ui_approve":               "Approve?",
		"ui_yes":                   "yes",
		"ui_no":                    "no",
		"ui_select_tool_details":   "select a tool to view details",
		"ui_status_label":          "Status",
		"ui_ok":                    "OK",
		"ui_failed":                "FAILED",
		"ui_duration":              "Duration",
		"ui_call_id":               "Call ID",
		"ui_arguments":             "Arguments",
		"ui_result":                "Result",
		"ui_none":                  "(none)",
		"ui_empty":                 "(empty)",
		"ui_control_deck":          "control deck",
		"ui_session_ready":         "session ready",
		"ui_controls":              "Controls",
		"ui_controls_line":         "Enter send  Tab focus  Ctrl+Shift+Tab half  Ctrl+T trace  Ctrl+S status  Ctrl+C quit",
		"ui_type_task":             "Type a task below and press Enter.",
		"dashboard_title":          "visual inspector",
		"dashboard_path":           "path",
		"dashboard_steps":          "STEPS",
		"dashboard_token":          "TOKEN",
		"dashboard_token_label":    "token",
		"dashboard_reasoning":      "REAS.",
		"dashboard_cost":           "COST ",
		"dashboard_velocity":       "velocity",
		"dashboard_model":          "model",
		"dashboard_tools":          "tools",
		"dashboard_compress":       "compress",
		"dashboard_health":         "health",
		"dashboard_io":             "io",
		"dashboard_state":          "state",
		"dashboard_idle":           "idle",
		"dashboard_last_step":      "last step",
		"dashboard_heartbeat":      "HEARTBEAT",
		"dashboard_cool":           "cool",
		"dashboard_warm":           "warm",
		"dashboard_hot":            "hot",
		"dashboard_stable":         "stable",
		"dashboard_critical":       "critical",
		"dashboard_pressure":       "compression-pressure",
		"dashboard_warming":        "warming",
		"dashboard_ready":          "ready",
		"dashboard_stalled":        "stalled",

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
		"status_scanning_context":  "analyse du contexte et des contraintes",
		"status_planning":          "préparation d'une approche propre",
		"status_looking_code":      "lecture du code",
		"status_searching_repo":    "recherche dans le dépôt",
		"status_running_tools":     "exécution des outils",
		"status_ready_next":        "prêt pour la suite",
		"status_response_done":     "réponse transmise",
		"status_standing_by":       "en attente",
		"status_executing_tool":    "exécution d'un appel d'outil",
		"status_collecting":        "collecte des éléments",
		"status_digging_files":     "exploration des fichiers",
		"status_stitching":         "assemblage des résultats d'outils",
		"status_cross_checking":    "vérification des résultats",
		"status_digesting":         "analyse des informations",
		"status_run_error":         "erreur détectée; consultez le transcript",
		"status_stalled":           "BLOQUÉ",

		// TUI chrome
		"ui_initializing_terminal": "Initialisation de la taille du terminal...",
		"ui_header_hint_wide":      "  Tab:focus  Shift+Tab:retour  Ctrl+PgUp/PgDn:moitié  Shift+flèches:redim.  Ctrl+T:trace  Ctrl+S:statut  Ctrl+M:inspecteur  Ctrl+D:détail  Ctrl+A:tout copier  Ctrl+C:quitter",
		"ui_header_hint_medium":    "  Tab:focus  Shift+Tab:retour  Ctrl+PgUp/PgDn:moitié  Ctrl+T:trace  Ctrl+S:statut  Ctrl+M:inspecteur  Ctrl+D:détail  Ctrl+A:tout copier  Ctrl+C:quitter",
		"ui_header_hint_narrow":    "  Tab:focus  Ctrl+PgUp/PgDn:moitié  Ctrl+T:trace  Ctrl+M:inspecteur  Ctrl+D:détail  Ctrl+A:tout copier  Ctrl+C:quitter",
		"ui_header_hint_tiny":      "  Tab:focus  Ctrl+PgUp/PgDn:moitié  Ctrl+M:inspecter  Ctrl+D:détail  Ctrl+A:copier  Ctrl+C:quitter",
		"ui_status":                "statut",
		"ui_trace":                 "trace",
		"ui_radio":                 "radio",
		"ui_feed":                  "flux",
		"ui_now":                   "maintenant",
		"ui_sub_agents":            "sous-agents: actifs=%d terminés=%d échoués=%d",
		"ui_current_agent":         "actuel: %s  %s",
		"ui_last_agent":            "dernier: %s",
		"ui_radio_idle":            "inactif",
		"ui_radio_playing":         "lecture",
		"ui_radio_controls":        "%s  %s  vol=%d%%  Ctrl+R lecture/arrêt  [/] volume  N/P station",
		"ui_custom":                "Personnalisé",
		"ui_dangerous_tool_call":   "APPEL D'OUTIL DANGEREUX",
		"ui_tool":                  "Outil",
		"ui_args":                  "Arguments",
		"ui_approve":               "Approuver?",
		"ui_yes":                   "oui",
		"ui_no":                    "non",
		"ui_select_tool_details":   "sélectionnez un outil pour voir les détails",
		"ui_status_label":          "Statut",
		"ui_ok":                    "OK",
		"ui_failed":                "ÉCHEC",
		"ui_duration":              "Durée",
		"ui_call_id":               "ID d'appel",
		"ui_arguments":             "Arguments",
		"ui_result":                "Résultat",
		"ui_none":                  "(aucun)",
		"ui_empty":                 "(vide)",
		"ui_control_deck":          "poste de contrôle",
		"ui_session_ready":         "session prête",
		"ui_controls":              "Contrôles",
		"ui_controls_line":         "Enter envoyer  Tab focus  Ctrl+Shift+Tab moitié  Ctrl+T trace  Ctrl+S statut  Ctrl+C quitter",
		"ui_type_task":             "Tapez une tâche ci-dessous puis appuyez sur Enter.",
		"dashboard_title":          "inspecteur visuel",
		"dashboard_path":           "chemin",
		"dashboard_steps":          "ÉTAPES",
		"dashboard_token":          "JETONS",
		"dashboard_token_label":    "jetons",
		"dashboard_reasoning":      "RAIS.",
		"dashboard_cost":           "COÛT ",
		"dashboard_velocity":       "vitesse",
		"dashboard_model":          "modèle",
		"dashboard_tools":          "outils",
		"dashboard_compress":       "compression",
		"dashboard_health":         "santé",
		"dashboard_io":             "e/s",
		"dashboard_state":          "état",
		"dashboard_idle":           "repos",
		"dashboard_last_step":      "dernière étape",
		"dashboard_heartbeat":      "BATTEMENT",
		"dashboard_cool":           "froid",
		"dashboard_warm":           "tiède",
		"dashboard_hot":            "chaud",
		"dashboard_stable":         "stable",
		"dashboard_critical":       "critique",
		"dashboard_pressure":       "pression de compression",
		"dashboard_warming":        "échauffement",
		"dashboard_ready":          "prêt",
		"dashboard_stalled":        "bloqué",

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
