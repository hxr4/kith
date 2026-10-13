package cli

import (
	"context"
	"time"

	"github.com/spf13/cobra"

	"kith/internal/daemon"
	"kith/internal/task"
)

// Dynamic shell completion backed by the running daemon (silent if it is down).

func (a *app) completionTasks(filter func(daemon.TaskView) bool) []string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var ts []daemon.TaskView
	if a.client().Call(ctx, "tasks", map[string]int{"limit": 200}, &ts) != nil {
		return nil
	}
	var out []string
	for _, t := range ts {
		if filter == nil || filter(t) {
			out = append(out, t.ID+"\t"+t.State+" "+t.Name+" ("+t.PeerName+")")
		}
	}
	return out
}

func (a *app) completeTasks(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return a.completionTasks(nil), cobra.ShellCompDirectiveNoFileComp
}

func (a *app) completeActiveTasks(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return a.completionTasks(func(t daemon.TaskView) bool { return !task.IsTerminal(t.State) }), cobra.ShellCompDirectiveNoFileComp
}

func (a *app) completePendingTasks(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return a.completionTasks(func(t daemon.TaskView) bool {
		return t.PendingApproval && t.Role == "executor" && t.State == task.Submitted
	}), cobra.ShellCompDirectiveNoFileComp
}

func (a *app) completePeers(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var ps []daemon.PeerView
	if a.client().Call(ctx, "peers", nil, &ps) != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var out []string
	for _, p := range ps {
		st := "offline"
		if p.Reachable {
			st = "online"
		}
		out = append(out, p.Name+"\t"+p.Display+" "+st)
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// completeArtifactPaths completes the second argument of `kith fetch`.
func (a *app) completeArtifactPaths(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) == 0 {
		return a.completionTasks(nil), cobra.ShellCompDirectiveNoFileComp
	}
	if len(args) > 1 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var as []daemon.ArtifactView
	if a.client().Call(ctx, "artifacts", map[string]string{"id": args[0]}, &as) != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var out []string
	for _, ar := range as {
		out = append(out, ar.Path+"\t"+ar.Status)
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}
