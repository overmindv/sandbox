package execution

import "strings"

func FileTextContains(command ExecutionCommand, needle string) bool {
	needle = strings.ToLower(strings.TrimSpace(needle))
	if needle == "" {
		return false
	}
	for _, file := range command.Files {
		if strings.Contains(strings.ToLower(string(file.Content)), needle) {
			return true
		}
	}
	return false
}
