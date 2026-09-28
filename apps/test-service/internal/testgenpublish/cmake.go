package testgenpublish

import "strings"

type cmakeCommand struct {
	name string
	args []string
}

// parseCMakeCommands recognizes the small literal-command subset needed to
// prove an existing generated test is linked. Unknown syntax fails closed.
func parseCMakeCommands(input string) ([]cmakeCommand, bool) {
	var commands []cmakeCommand
	i := 0
	skip := func() {
		for i < len(input) {
			if input[i] == '#' {
				for i < len(input) && input[i] != '\n' {
					i++
				}
			} else if input[i] == ' ' || input[i] == '\t' || input[i] == '\r' || input[i] == '\n' {
				i++
			} else {
				break
			}
		}
	}
	for {
		skip()
		if i == len(input) {
			return commands, true
		}
		start := i
		for i < len(input) && (input[i] >= 'a' && input[i] <= 'z' || input[i] >= 'A' && input[i] <= 'Z' || input[i] == '_' || i > start && input[i] >= '0' && input[i] <= '9') {
			i++
		}
		if start == i {
			return nil, false
		}
		name := strings.ToLower(input[start:i])
		// Conditional and indirection commands make a textual link insufficient
		// evidence that the selected source participates in the build.
		switch name {
		case "if", "elseif", "else", "endif", "function", "endfunction", "macro", "endmacro", "foreach", "endforeach", "while", "endwhile", "include", "add_subdirectory", "cmake_language", "block", "endblock":
			return nil, false
		}
		skip()
		if i == len(input) || input[i] != '(' {
			return nil, false
		}
		i++
		command := cmakeCommand{name: name}
		for {
			skip()
			if i == len(input) {
				return nil, false
			}
			if input[i] == ')' {
				i++
				break
			}
			if input[i] == '"' {
				i++
				var value strings.Builder
				closed := false
				for i < len(input) {
					if input[i] == '"' {
						i++
						closed = true
						break
					}
					if input[i] == '\\' {
						i++
						if i == len(input) {
							return nil, false
						}
					}
					value.WriteByte(input[i])
					i++
				}
				if !closed || i < len(input) && input[i] != ')' && input[i] != '#' && input[i] != ' ' && input[i] != '\t' && input[i] != '\r' && input[i] != '\n' {
					return nil, false
				}
				command.args = append(command.args, value.String())
				continue
			}
			start = i
			for i < len(input) && input[i] != ')' && input[i] != '#' && input[i] != ' ' && input[i] != '\t' && input[i] != '\r' && input[i] != '\n' {
				if input[i] == '(' || input[i] == '"' || input[i] == '[' || input[i] == ']' || input[i] == ';' || input[i] == '\\' {
					return nil, false
				}
				i++
			}
			if start == i {
				return nil, false
			}
			command.args = append(command.args, input[start:i])
		}
		commands = append(commands, command)
	}
}

func cmakeHasCommand(commands []cmakeCommand, name string, args ...string) bool {
	for _, command := range commands {
		if command.name != name || len(command.args) != len(args) {
			continue
		}
		match := true
		for i, arg := range args {
			if command.args[i] != arg {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
