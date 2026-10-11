package remarkable

import (
	"fmt"
	"os"
	"strings"
)

const chiappaMachine = "reMarkable Chiappa"

// detectChiappa checks that one of the paths names a Paper Pro Move. It
// returns the matching name, or an error listing what each path held.
func detectChiappa(machinePath, deviceTreePath string) (string, error) {
	paths := []string{machinePath, deviceTreePath}
	var problems []string
	found := ""
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", path, err))
			continue
		}
		name := strings.TrimSpace(strings.TrimRight(string(data), "\x00"))
		if name == "" {
			problems = append(problems, fmt.Sprintf("%s: empty", path))
			continue
		}
		if strings.EqualFold(name, chiappaMachine) {
			return name, nil
		}
		if found == "" {
			found = name
		}
		problems = append(problems, fmt.Sprintf("%s: %q", path, name))
	}
	if found != "" {
		return found, fmt.Errorf("unsupported device (%s); Move build requires %q", strings.Join(problems, "; "), chiappaMachine)
	}
	return "", fmt.Errorf("cannot detect device (%s)", strings.Join(problems, "; "))
}
