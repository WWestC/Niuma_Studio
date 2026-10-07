// wiregen — the frame-contract generator (thin CLI over tools/wiregen/gen).
// Static extraction (go/ast), not reflect: constants are invisible to
// runtime reflection. One run regenerates every committed artifact of
// the mirror discipline:
//
//	web/wire/gen/frame-contract.json   frames + typed field shapes + envelope
//	web/wire/gen/types.js              JSDoc full mirrors (payload family)
//	<domain>/wireconv_gen.go           mirror conversions (five domain pkgs)
//
// Change wire/wire.go, a domain payload, or the envelope — re-run
// `go run ./tools/wiregen`, commit the regenerated files. The
// freshness tests in wire/ re-run the same library in-process and
// byte-compare, so a forgotten run fails the suite with these paths.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/WWestC/Niuma_Studio/tools/wiregen/gen"
)

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	c, err := gen.BuildContract(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	writes := 0

	jsonBytes, err := gen.RenderContractJSON(c)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	dest := filepath.Join(root, "web", "wire", "gen", "frame-contract.json")
	if err := os.WriteFile(dest, jsonBytes, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	writes++
	fmt.Printf("wiregen: %d consts, %d types, %d envelope fields → %s\n",
		len(c.Frames), len(c.Types), len(c.Envelope), dest)

	convs, err := gen.GenWireconv(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "wireconv:", err)
		os.Exit(1)
	}
	for _, path := range sortedPaths(convs) {
		if err := os.WriteFile(filepath.Join(root, path), convs[path], 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		writes++
		fmt.Printf("wiregen: %d bytes → %s\n", len(convs[path]), path)
	}

	types := gen.GenTypedefs(c)
	td := filepath.Join(root, "web", "wire", "gen", "types.js")
	if err := os.WriteFile(td, types, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	writes++
	fmt.Printf("wiregen: %d typedefs → %s\n", len(gen.TypedefNames), td)
	fmt.Printf("wiregen: %d files regenerated\n", writes)
}

func sortedPaths(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
