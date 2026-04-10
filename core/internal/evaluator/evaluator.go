package evaluator

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"github.com/google/cel-go/common/types/traits"
)

// IsResourceProtected queries the protected_resources table using glob matching
func IsResourceProtected(db *sql.DB, path string) bool {
	rows, err := db.Query("SELECT pattern FROM protected_resources")
	if err != nil {
		return false
	}
	defer rows.Close()

	for rows.Next() {
		var pattern string
		if err := rows.Scan(&pattern); err != nil {
			continue
		}

		// Standard glob match
		matched, err := filepath.Match(pattern, path)
		if err == nil && matched {
			return true
		}

		// Suffix/contains fallback for partial paths
		if strings.HasSuffix(path, pattern) || strings.Contains(path, "/"+pattern) || path == pattern {
			return true
		}
	}
	return false
}

// CreateCELEnv builds the CEL environment with all Four Guards variables and the is_protected() helper
func CreateCELEnv(db *sql.DB) (*cel.Env, error) {
	return cel.NewEnv(
		cel.Variable("args", cel.ListType(cel.StringType)),
		cel.Variable("payload", cel.StringType),
		cel.Variable("depth", cel.IntType),
		cel.Variable("server", cel.StringType),
		cel.Variable("tool", cel.StringType),
		cel.Variable("mcp_args", cel.MapType(cel.StringType, cel.DynType)),

		// Custom function: is_protected_file(path) -> bool
		// Queries protected_resources table for glob matching
		cel.Function("is_protected_file",
			cel.Overload("is_protected_file_string",
				[]*cel.Type{cel.StringType},
				cel.BoolType,
				cel.UnaryBinding(func(arg ref.Val) ref.Val {
					pathVal, ok := arg.(types.String)
					if !ok {
						return types.Bool(false)
					}
					return types.Bool(IsResourceProtected(db, string(pathVal)))
				}),
			),
		),

		// Custom member function: args.contains(string) -> bool
		// Extends CEL List type natively to support .contains() natively
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
}

// EvaluateRule compiles and evaluates a CEL condition using the unwrapped CommandContext
func EvaluateRule(env *cel.Env, condition string, cmdCtx CommandContext, serverID, toolName string) (bool, error) {
	ast, issues := env.Compile(condition)
	if issues != nil && issues.Err() != nil {
		return false, issues.Err()
	}
	prg, err := env.Program(ast)
	if err != nil {
		return false, err
	}

	out, _, err := prg.Eval(map[string]interface{}{
		"args":    cmdCtx.Args,
		"payload": cmdCtx.ScriptPayload,
		"depth":   cmdCtx.Depth,
		"server":  serverID,
		"tool":    toolName,
	})
	if err != nil {
		return false, err
	}

	if out.Type() == types.BoolType {
		return out.Value().(bool), nil
	}
	if out.Type() == types.StringType {
		strVal := out.Value().(string)
		if strVal == "true" {
			return true, nil
		} else if strVal == "false" {
			return false, nil
		}
	}
	return false, fmt.Errorf("evaluation did not return a boolean")
}
