package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"kith/internal/config"
	"kith/internal/daemon"
	"kith/internal/identity"
	"kith/internal/svc"
	"kith/internal/task"
)

// statusCmd prints a one-screen overview.
func (a *app) statusCmd() *cobra.Command {
	return &cobra.Command{Use: "status", Short: "Overview: daemon, identity, peers, active tasks, approvals", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home := config.ResolveHome(a.home)
			service, _ := svc.Status(home)
			var st daemon.StatusView
			derr := a.call(cmd, "status", nil, &st)
			var peers []daemon.PeerView
			var ts []daemon.TaskView
			if derr == nil {
				a.call(cmd, "peers", nil, &peers)
				a.call(cmd, "tasks", map[string]int{"limit": 100000}, &ts)
			}
			if a.jsonOut {
				a.printJSON(map[string]any{"running": derr == nil, "service": service, "daemon": st, "peers": peers,
					"active": filterTasks(ts, taskFilter{active: true}), "pending": filterTasks(ts, taskFilter{pending: true})})
				return nil
			}
			label := func(s string) string { return a.p.dim(padRight(s, 9)) }
			if derr != nil {
				fmt.Fprintf(a.out, "%s%s  (service: %s)\n", label("daemon"), a.p.red("not running"), service)
				fmt.Fprintf(a.out, "%sstart it with `kith daemon start` (background) or `kith daemon install` (autostart)\n", label(""))
				if id, err := identity.LoadOrCreate(home); err == nil {
					fmt.Fprintf(a.out, "%s%s\n", label("this"), identity.Display(id.FP))
				}
				return nil
			}
			fmt.Fprintf(a.out, "%s%s  pid %d · TCP %d · %s · service: %s\n", label("daemon"), a.p.green("running"), st.PID, st.Port, versionString(), service)
			fmt.Fprintf(a.out, "%s%s  %s\n", label("this"), a.p.bold(st.Name), st.Display)
			if len(peers) == 0 {
				fmt.Fprintf(a.out, "%s%s\n", label("peers"), a.p.dim("none — run `kith pair` on both machines"))
			}
			for i, p := range peers {
				l := ""
				if i == 0 {
					l = "peers"
				}
				fmt.Fprintf(a.out, "%s%s  %s  %s  %s\n", label(l), padRight(a.p.bold(p.Name), 16), padRight(a.p.online(p.Reachable), 7), p.Display, p.Caps)
			}
			active := filterTasks(ts, taskFilter{active: true})
			pending := filterTasks(ts, taskFilter{pending: true})
			if len(active) == 0 {
				fmt.Fprintf(a.out, "%s%s\n", label("tasks"), a.p.dim("no active tasks"))
			}
			for i, t := range active {
				l := ""
				if i == 0 {
					l = "tasks"
				}
				fmt.Fprintf(a.out, "%s%s  %s %-8s %s  %s\n", label(l), t.ID, roleArrow(t.Role), t.PeerName, padRight(a.p.state(t.State), 12), t.Name)
			}
			if len(pending) > 0 {
				var ids []string
				for _, t := range pending {
					ids = append(ids, t.ID)
				}
				fmt.Fprintf(a.out, "%s%s %s → `kith approve` to review\n", label("approval"),
					a.p.yellow(fmt.Sprintf("%d task(s) waiting:", len(pending))), strings.Join(ids, ", "))
			}
			done := 0
			for _, t := range ts {
				if t.State == task.Completed {
					done++
				}
			}
			fmt.Fprintf(a.out, "%s%d tasks total, %d completed · `kith tasks` for details\n", label("history"), len(ts), done)
			return nil
		}}
}
