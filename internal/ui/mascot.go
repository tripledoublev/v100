package ui

// Mascot returns the ASCII art character with an expression matching the mode.
func Mascot(mode, personality string) string {
	switch personality {
	case "glitch-ghost":
		return mascotGlitch(mode)
	case "monolith":
		return mascotMonolith(mode)
	default:
		return mascotClassic(mode)
	}
}

func mascotClassic(mode string) string {
	var eyes string
	var mouth string

	switch mode {
	case "thinking":
		eyes = "o O"
		mouth = "・"
	case "tooling":
		eyes = "> <"
		mouth = "▼"
	case "error":
		eyes = "O O"
		mouth = "▽"
	default:
		eyes = "- -"
		mouth = "‿"
	}

	return `     /_/_          ` + eyes + `
    (o o)         ┌───────┐
    ( : )         │ v100  │
    m"m"m         └───────┘
     ` + mouth + `        your coding companion
`
}

func mascotGlitch(mode string) string {
	eyes := "x X"
	if mode == "thinking" {
		eyes = "? ?"
	}
	return `     .----------.
    /  _      _  \    ` + eyes + `
   |  / \    / \  |  ┌────────┐
   |  | |    | |  |  │ GLITCH │
   |  \_/    \_/  |  └────────┘
    \            /
     '----------'
`
}

func mascotMonolith(mode string) string {
	symbol := "■"
	if mode == "thinking" {
		symbol = "░"
	} else if mode == "tooling" {
		symbol = "▓"
	}
	return `    .----------.
    |          |
    |    ` + symbol + `     |    REASONING...
    |          |
    '----------'
`
}

// MascotIdle returns the ASCII robot with idle expression.
func MascotIdle(personality string) string {
	return Mascot("idle", personality)
}

// MascotThinking returns the ASCII robot with thinking expression.
func MascotThinking(personality string) string {
	return Mascot("thinking", personality)
}

// MascotTooling returns the ASCII robot with tooling expression.
func MascotTooling(personality string) string {
	return Mascot("tooling", personality)
}

// MascotError returns the ASCII robot with error expression.
func MascotError(personality string) string {
	return Mascot("error", personality)
}
