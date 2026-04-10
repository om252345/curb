package cmd

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"github.com/google/cel-go/common/types/traits"
	"github.com/om252345/curb/internal/config"
	"github.com/spf13/cobra"
)

// evaluateCmd is called by wrapper scripts in ~/.curb/bin/ to check if a command is allowed.
// Exit code 0 = allowed, exit code 1 = blocked (reason printed to stderr).
var evaluateCmd = &cobra.Command{
	Use:                "evaluate <command> [args...]",
	Short:              "Evaluate a CLI command against .curb.yml rules",
	Hidden:             true,
	DisableFlagParsing: true,
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) == 0 {
			fmt.Fprintln(os.Stdout, "allow")
			os.Exit(0)
		}
		command := args[0]
		cmdArgs := args[1:]

		// Load config
		cfg, err := config.LoadConfig(config.ConfigPaths()...)
		if err != nil {
			fmt.Fprintln(os.Stdout, "allow")
			os.Exit(0)
		}

		// Create CEL environment with custom list.contains() — same as evaluator.go
		celEnv, err := cel.NewEnv(
			cel.Variable("args", cel.ListType(cel.StringType)),
			cel.Variable("command", cel.StringType),

			// Custom: args.contains("push") → bool
			cel.Function("contains",
				cel.MemberOverload("list_contains_string",
					[]*cel.Type{cel.ListType(cel.StringType), cel.StringType},
					cel.BoolType,
					cel.BinaryBinding(func(list ref.Val, elem ref.Val) ref.Val {
						l, ok := list.(traits.Lister)
						if !ok {
							return types.Bool(false)
						}
						it := l.Iterator()
						for it.HasNext() == types.True {
							val := it.Next()
							if val.Equal(elem) == types.True {
								return types.Bool(true)
							}
						}
						return types.Bool(false)
					}),
				),
			),
		)
		if err != nil {
			fmt.Fprintln(os.Stdout, "allow")
			os.Exit(0)
		}

		// Build args as string list for CEL
		celArgs := make([]string, len(cmdArgs))
		copy(celArgs, cmdArgs)

		// Evaluate each matching rule
		for _, rule := range cfg.CLI.Rules {
			if rule.Command != command {
				continue
			}

			ast, issues := celEnv.Compile(rule.Condition)
			if issues != nil && issues.Err() != nil {
				continue
			}

			prg, err := celEnv.Program(ast)
			if err != nil {
				continue
			}

			out, _, err := prg.Eval(map[string]interface{}{
				"args":    celArgs,
				"command": command,
			})
			if err != nil {
				continue
			}

			if outVal, ok := out.Value().(bool); ok && outVal {
				if rule.Action == "block" {
					fmt.Fprintf(os.Stderr, "\033[31m[Curb] Blocked: %s %s — %s\033[0m\n",
						command, strings.Join(cmdArgs, " "), rule.Name)
					fmt.Fprintln(os.Stdout, "block")
					os.Exit(1)
				} else if rule.Action == "ask" || rule.Action == "hitl" {
					// HITL interception workflow
					ipcSock := os.Getenv("CURB_RUN_IPC")
					if ipcSock != "" {
						conn, err := net.Dial("unix", ipcSock)
						if err == nil {
							fmt.Fprintf(conn, "ASK %s|%s %s\n", rule.Name, command, strings.Join(cmdArgs, " "))

							reader := bufio.NewReader(conn)
							response, _ := reader.ReadString('\n')
							response = strings.TrimSpace(response)
							conn.Close()

							if response == "ALLOW" {
								fmt.Fprintln(os.Stdout, "allow")
								os.Exit(0)
							} else {
								fmt.Fprintf(os.Stderr, "\033[31m[Curb] Blocked via HITL: %s %s\033[0m\n", command, strings.Join(cmdArgs, " "))
								fmt.Fprintln(os.Stdout, "block")
								os.Exit(1)
							}
						}
					}

					// Fallback if no IPC (e.g. not running under 'curb run')
					fmt.Fprintf(os.Stderr, "\033[31m[Curb] Blocked: %s %s — %s (HITL required but not in 'curb run' mode)\033[0m\n",
						command, strings.Join(cmdArgs, " "), rule.Name)
					fmt.Fprintln(os.Stdout, "block")
					os.Exit(1)
				}
			}
		}

		fmt.Fprintln(os.Stdout, "allow")
		os.Exit(0)
	},
}

func init() {
	rootCmd.AddCommand(evaluateCmd)
}
