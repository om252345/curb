package evaluator

import (
	"bytes"
	"encoding/base64"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

type CommandContext struct {
	Raw           string
	Leaf          string
	Depth         int
	Args          []string
	IsObfuscated  bool
	ScriptPayload string
}

func PeelCommand(raw string) CommandContext {
	ctx := CommandContext{
		Raw:           raw,
		Depth:         0,
		Args:          []string{},
		ScriptPayload: "",
		IsObfuscated:  false,
	}

	currentCmdStr := raw

	for {
		p := syntax.NewParser()
		file, err := p.Parse(strings.NewReader(currentCmdStr), "")
		if err != nil || file == nil || len(file.Stmts) == 0 {
			ctx.Leaf = currentCmdStr
			break
		}

		stmt := file.Stmts[0]

		// Check for pipe into base64 --decode (obfuscation detection)
		if pipe, ok := stmt.Cmd.(*syntax.BinaryCmd); ok && pipe.Op == syntax.Pipe {
			var rightPrinter bytes.Buffer
			syntax.NewPrinter().Print(&rightPrinter, pipe.Y)
			rStr := rightPrinter.String()

			if strings.Contains(rStr, "base64") && (strings.Contains(rStr, "-d") || strings.Contains(rStr, "--decode")) {
				ctx.IsObfuscated = true

				var leftPrinter bytes.Buffer
				syntax.NewPrinter().Print(&leftPrinter, pipe.X)
				lStr := strings.TrimSpace(leftPrinter.String())

				if strings.HasPrefix(lStr, "echo") {
					b64Str := strings.TrimSpace(strings.TrimPrefix(lStr, "echo"))
					b64Str = strings.Trim(b64Str, " '\"")
					decoded, err := base64.StdEncoding.DecodeString(b64Str)
					if err == nil {
						ctx.Depth++
						currentCmdStr = string(decoded)
						ctx.ScriptPayload = currentCmdStr
						continue
					}
				}
			}
			ctx.Leaf = currentCmdStr
			break
		}

		call, ok := stmt.Cmd.(*syntax.CallExpr)
		if !ok || len(call.Args) == 0 {
			ctx.Leaf = currentCmdStr
			break
		}

		var args []string
		for _, arg := range call.Args {
			var buf bytes.Buffer
			syntax.NewPrinter().Print(&buf, arg)
			s := buf.String()
			// Only trim matching outer quotes
			if (strings.HasPrefix(s, "\"") && strings.HasSuffix(s, "\"")) ||
				(strings.HasPrefix(s, "'") && strings.HasSuffix(s, "'")) {
				s = s[1 : len(s)-1]
			}
			args = append(args, s)
		}

		if len(args) > 0 {
			ctx.Leaf = args[0]
			ctx.Args = args

			// Recursive unwrapping: sh -c, bash -c, python3 -c
			if (ctx.Leaf == "sh" || ctx.Leaf == "bash" || ctx.Leaf == "python3") && len(args) >= 3 && args[1] == "-c" {
				currentCmdStr = args[2]
				ctx.ScriptPayload = currentCmdStr
				ctx.Depth++
				continue
			}

			// Recursive unwrapping: node -e
			if ctx.Leaf == "node" && len(args) >= 3 && args[1] == "-e" {
				currentCmdStr = args[2]
				ctx.ScriptPayload = currentCmdStr
				ctx.Depth++
				continue
			}
		} else {
			ctx.Leaf = currentCmdStr
		}

		break
	}

	return ctx
}
