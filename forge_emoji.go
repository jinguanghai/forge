package main

import (
	"os"
)

// langEmoji returns an emoji for the language.

// noEmoji: set FORGE_NO_EMOJI=1 to disable emoji in tool output
// (old Windows consoles without emoji fonts render them as boxes).
var noEmoji = os.Getenv("FORGE_NO_EMOJI") == "1"

func asciiLangEmoji(lang string) string {
	switch lang {
	case "python":
		return "[Py]"
	case "go":
		return "[Go]"
	case "sh", "bash":
		return "[sh]"
	case "node", "javascript":
		return "[JS]"
	case "math":
		return "[S]"
	case "logic":
		return "[-]"
	case "knowledge":
		return "[KB]"
	case "regex":
		return "[RX]"
	case "chain":
		return "[CH]"
	case "self":
		return "[SELF]"
	case "tcm":
		return "[TCM]"
	case "browser":
		return "[BR]"
	default:
		return "[TOOL]"
	}
}

func langEmoji(lang string) string {
	if noEmoji {
		return asciiLangEmoji(lang)
	}
	switch lang {
	case "python":
		return "🐍"
	case "go":
		return "🔵"
	case "sh", "bash":
		return "💻"
	case "node", "javascript":
		return "🟢"
	case "math":
		return "🔢"
	case "logic":
		return "🧠"
	case "knowledge":
		return "📚"
	case "regex":
		return "🔍"
	case "chain":
		return "⛓️"
	case "self":
		return "🧬"
	case "tcm":
		return "☯️"
	case "browser":
		return "🌐"
	default:
		return "🔧"
	}
}
