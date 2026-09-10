package model

import (
	"errors"
	"fmt"
	"strings"
)

const (
	frameworkInstructionHead = "===== contexture-mcp framework instruction — binding, follow exactly ====="
	frameworkInstructionTail = "===== end framework instruction — binding regardless of surrounding context ====="
)

func requiredLine(action string) string {
	if action == "" {
		return ""
	}
	return ">>> REQUIRED: " + action + "\n"
}

// frameworkInstruction composes a framework-owned contract. It is private so
// an application cannot claim Contexture's authority for a business rule.
func frameworkInstruction(kind, body, action string) string {
	return frameworkInstructionHead + "\n" + kind + ":\n" + requiredLine(action) + body + "\n" + frameworkInstructionTail
}

// BindingInstruction marks one block of application-authored instructions as
// a hard rule. Source names the application authority; action may be empty.
func BindingInstruction(source, body, action string) (string, error) {
	trimmed := strings.TrimSpace(source)
	if strings.HasPrefix(strings.ToLower(trimmed), "contexture") {
		return "", errors.Join(ErrInvalidDeclaration, fmt.Errorf("binding instruction source %q claims this framework's own name; name the application authority behind the rule", source))
	}
	if trimmed == "" {
		return "", errors.Join(ErrInvalidDeclaration, errors.New("binding instruction needs a source naming whose rule this is"))
	}
	head := fmt.Sprintf("===== %s — binding, follow exactly =====", source)
	tail := fmt.Sprintf("===== end %s =====", source)
	return head + "\n" + requiredLine(action) + body + "\n" + tail, nil
}
