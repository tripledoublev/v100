package ui

// Mascot returns the ASCII art robot with an expression matching the given status mode.
// The robot has different "eyes" and expressions based on: idle, thinking, tooling, error.
func Mascot(mode string) string {
	var eyes string
	var mouth string

	switch mode {
	case "thinking":
		// Questioning/dizzy eyes for thinking
		eyes = "o O"
		mouth = "・"
	case "tooling":
		// Active/working eyes for tooling
		eyes = "> <"
		mouth = "▼"
	case "error":
		// Surprised/concerned eyes for error
		eyes = "O O"
		mouth = "▽"
	default:
		// Default calm eyes for idle
		eyes = "- -"
		mouth = "‿"
	}

	return `     /_/_          ` + eyes + `
    (o o)         ┌───────┐
    ( : )         │ HELLO │
    m"m"m         └───────┘
     ` + mouth + `        I am v100
                your coding companion
`
}

// MascotIdle returns the ASCII robot with idle expression.
func MascotIdle() string {
	return Mascot("idle")
}

// MascotThinking returns the ASCII robot with thinking expression.
func MascotThinking() string {
	return Mascot("thinking")
}

// MascotTooling returns the ASCII robot with tooling expression.
func MascotTooling() string {
	return Mascot("tooling")
}

// MascotError returns the ASCII robot with error expression.
func MascotError() string {
	return Mascot("error")
}
