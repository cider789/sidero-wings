package firewall

import (
	"bytes"
	"context"
	"os/exec"
	"runtime"
)

type NftBackend struct{ path string }

func NewNftBackend() *NftBackend      { path, _ := exec.LookPath("nft"); return &NftBackend{path: path} }
func (b *NftBackend) Available() bool { return runtime.GOOS == "linux" && b.path != "" }
func (b *NftBackend) DryRun(ctx context.Context, rules []AppliedRule) error {
	return b.run(ctx, rules, true)
}
func (b *NftBackend) Apply(ctx context.Context, rules []AppliedRule) error {
	return b.run(ctx, rules, false)
}

func (b *NftBackend) run(ctx context.Context, rules []AppliedRule, check bool) error {
	if !b.Available() {
		return ErrUnsupported
	}
	exists := exec.CommandContext(ctx, b.path, "list", "table", "inet", "sidero_wings").Run() == nil
	script, err := RenderRuleset(rules, exists)
	if err != nil {
		return err
	}
	args := []string{"-f", "-"}
	if check {
		args = []string{"-c", "-f", "-"}
	}
	command := exec.CommandContext(ctx, b.path, args...)
	command.Stdin = bytes.NewBufferString(script)
	if err := command.Run(); err != nil {
		return err
	}
	return nil
}
