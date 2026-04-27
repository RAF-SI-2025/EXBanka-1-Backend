// proto-idempotency-check walks every .proto file under contract/proto and
// asserts that any rpc preceded by a `// idempotent` comment has a request
// message containing a field declared as `string idempotency_key = N;`.
//
// The marker comment is the contract: any saga-step gRPC call that may be
// retried (after crash, after compensator restart, after caller timeout)
// MUST flow through the IdempotencyRepository.Run cache, and the cache key
// has to ride on the request itself. This tool catches drift where a new
// idempotent rpc forgets the field, or where a refactor accidentally drops
// it.
//
// Usage: `go run ./tools/proto-idempotency-check [root]`. With no argument
// it scans `contract/proto`. Exits 0 on success, 1 if any violation is
// found, 2 on a walk error.
package main

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	rpcRe         = regexp.MustCompile(`^\s*rpc\s+\w+\s*\(\s*(\w+)\s*\)\s*returns\s*\(`)
	idempMarkerRe = regexp.MustCompile(`^\s*//\s*idempotent\s*$`)
	idempFieldRe  = regexp.MustCompile(`^\s*string\s+idempotency_key\s*=\s*\d+\s*;`)
)

func main() {
	root := "contract/proto"
	if len(os.Args) > 1 {
		root = os.Args[1]
	}

	var violations []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".proto" {
			return nil
		}

		markedReqs := scanForIdempotentRPCs(path)
		for reqType, line := range markedReqs {
			if !messageHasIdempotencyKey(path, reqType) {
				violations = append(violations,
					fmt.Sprintf("%s:%d: rpc declared idempotent but request %s lacks `string idempotency_key = N;`", path, line, reqType))
			}
		}
		return nil
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if len(violations) > 0 {
		for _, v := range violations {
			fmt.Fprintln(os.Stderr, v)
		}
		os.Exit(1)
	}
	fmt.Println("OK: all idempotent rpcs have idempotency_key on their request")
}

// scanForIdempotentRPCs returns the request-type name for every rpc in
// `path` whose previous non-blank line is `// idempotent`. The returned
// line number is the rpc declaration line so violations point at the rpc,
// not at the marker comment.
func scanForIdempotentRPCs(path string) map[string]int {
	out := map[string]int{}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	var prev string
	line := 0
	for sc.Scan() {
		line++
		cur := sc.Text()
		if m := rpcRe.FindStringSubmatch(cur); m != nil {
			if idempMarkerRe.MatchString(prev) {
				out[m[1]] = line
			}
		}
		if strings.TrimSpace(cur) != "" {
			prev = cur
		}
	}
	return out
}

// messageHasIdempotencyKey returns true iff `path` declares a `message
// msgName { ... }` block containing a `string idempotency_key = N;` field
// before the closing brace. The scan is intentionally simple: it does not
// handle nested messages within a target body, but the saga-callee request
// messages used in this codebase are flat, so this is sufficient.
func messageHasIdempotencyKey(path, msgName string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	inMsg := false
	msgRe := regexp.MustCompile(`^\s*message\s+` + regexp.QuoteMeta(msgName) + `\s*\{`)
	for sc.Scan() {
		cur := sc.Text()
		if !inMsg && msgRe.MatchString(cur) {
			inMsg = true
			continue
		}
		if inMsg {
			if strings.Contains(cur, "}") {
				return false
			}
			if idempFieldRe.MatchString(cur) {
				return true
			}
		}
	}
	return false
}
