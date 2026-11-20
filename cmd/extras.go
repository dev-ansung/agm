package cmd

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/dev-ansung/agm/internal/aliases"
	"github.com/dev-ansung/agm/internal/api"
	"github.com/dev-ansung/agm/internal/credstore"
	"github.com/dev-ansung/agm/internal/crypto"
	"github.com/dev-ansung/agm/internal/db"
	"github.com/dev-ansung/agm/internal/paths"
	"github.com/dev-ansung/agm/internal/process"
	"github.com/dev-ansung/agm/internal/target"
	"github.com/spf13/cobra"
)

var (
	autoSwitchMin     int
	autoSwitchModel   string
	autoSwitchTarget  string
	autoSwitchRefresh bool
	autoSwitchJSON    bool
	autoSwitchYes     bool
	watchInterval     int
)

func init() {
	RootCmd.AddCommand(aliasCmd)
	RootCmd.AddCommand(unaliasCmd)
	RootCmd.AddCommand(exportCmd)
	RootCmd.AddCommand(importBackupCmd)

	autoSwitchCmd.Flags().IntVar(&autoSwitchMin, "min", 50, "Minimum quota score percentage")
	autoSwitchCmd.Flags().IntVar(&autoSwitchMin, "min-quota", 50, "Minimum quota score percentage")
	autoSwitchCmd.Flags().StringVar(&autoSwitchModel, "model", "", "Filter by model name (gemini|claude)")
	autoSwitchCmd.Flags().StringVarP(&autoSwitchTarget, "target", "t", "all", "Apply account to target product(s) (agy|ide|all)")
	autoSwitchCmd.Flags().BoolVar(&autoSwitchRefresh, "refresh", false, "Refresh live quotas before selecting an account")
	autoSwitchCmd.Flags().BoolVar(&autoSwitchJSON, "json", false, "Output JSON (implies non-interactive when combined with --yes)")
	autoSwitchCmd.Flags().BoolVarP(&autoSwitchYes, "yes", "y", false, "Confirm switch without prompting")
	_ = autoSwitchCmd.RegisterFlagCompletionFunc("model", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return []string{"gemini", "claude"}, cobra.ShellCompDirectiveNoFileComp
	})
	_ = autoSwitchCmd.RegisterFlagCompletionFunc("target", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return []string{"all", "ide", "agy"}, cobra.ShellCompDirectiveNoFileComp
	})
	RootCmd.AddCommand(autoSwitchCmd)

	RootCmd.AddCommand(doctorCmd)

	watchCmd.Flags().IntVarP(&watchInterval, "interval", "n", 10, "Refresh interval in seconds")
	RootCmd.AddCommand(watchCmd)
}

var aliasCmd = &cobra.Command{
	Use:   "alias [name] [email]",
	Short: "List or set aliases",
	Args:  cobra.MaximumNArgs(2),
	ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		if len(args) == 1 {
			var completions []string
			accounts, err := db.ListAccounts()
			if err == nil {
				for _, acc := range accounts {
					desc := "Account"
					if acc.Name != "" {
						desc = acc.Name
					}
					completions = append(completions, acc.Email+"\t"+desc)
				}
			}
			return completions, cobra.ShellCompDirectiveNoFileComp
		}
		return nil, cobra.ShellCompDirectiveNoFileComp
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			m := aliases.Load()
			if len(m) == 0 {
				fmt.Println("No aliases set.")
				return nil
			}
			fmt.Printf("%-16s %s\n", "ALIAS", "EMAIL")
			for k, v := range m {
				fmt.Printf("%-16s %s\n", k, v)
			}
			return nil
		}
		if len(args) == 1 {
			return fmt.Errorf("usage: agm alias <name> <email>")
		}
		if err := aliases.Set(args[0], args[1]); err != nil {
			return err
		}
		fmt.Printf("Alias '%s' → %s\n", args[0], args[1])
		return nil
	},
}

var unaliasCmd = &cobra.Command{
	Use:   "unalias <name>",
	Short: "Remove an alias",
	Args:  cobra.ExactArgs(1),
	ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var completions []string
		for alias, email := range aliases.Load() {
			completions = append(completions, alias+"\t"+"Alias for "+email)
		}
		return completions, cobra.ShellCompDirectiveNoFileComp
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		if !aliases.Remove(args[0]) {
			return fmt.Errorf("alias %q not found", args[0])
		}
		fmt.Printf("Removed alias '%s'\n", args[0])
		return nil
	},
}

var exportCmd = &cobra.Command{
	Use:   "export [file]",
	Short: "Export accounts to JSON",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		out := "accounts_backup.json"
		if len(args) > 0 {
			out = args[0]
		}
		if err := db.ExportRawAccounts(out); err != nil {
			return err
		}
		fmt.Printf("Exported accounts to %s\n", out)
		return nil
	},
}

var importBackupCmd = &cobra.Command{
	Use:     "import-backup <file>",
	Aliases: []string{"import"},
	Short:   "Import accounts from backup",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		n, err := db.ImportRawAccounts(args[0])
		if err != nil {
			return err
		}
		fmt.Printf("Imported %d account(s).\n", n)
		return nil
	},
}

var autoSwitchCmd = &cobra.Command{
	Use:   "auto-switch",
	Short: "Switch to the best account based on available quota",
	Long:  "Scores each stored account by its lowest remaining quota across matching models (--model), then switches --target to the highest-scoring account >= --min.",
	RunE: func(cmd *cobra.Command, args []string) error {
		tgt, err := target.Parse(autoSwitchTarget)
		if err != nil {
			return err
		}
		accounts, err := db.ListAccounts()
		if err != nil {
			return err
		}
		if autoSwitchRefresh && len(accounts) > 0 {
			for i := range accounts {
				if err := api.RefreshAccountQuota(&accounts[i]); err != nil {
					fmt.Fprintf(os.Stderr, "warning: refresh %s: %v\n", accounts[i].Email, err)
				}
			}
			accounts, err = db.ListAccounts()
			if err != nil {
				return err
			}
		}
		var best *db.Account
		bestScore := -1
		filter := strings.ToLower(autoSwitchModel)
		for i := range accounts {
			a := &accounts[i]
			if a.Quota == nil || len(a.Quota.Models) == 0 {
				continue
			}
			var vals []int
			for name, m := range a.Quota.Models {
				if filter != "" && !strings.Contains(strings.ToLower(name), filter) {
					continue
				}
				vals = append(vals, m.Percentage)
			}
			if len(vals) == 0 {
				continue
			}
			score := vals[0]
			for _, v := range vals[1:] {
				if v < score {
					score = v
				}
			}
			if score >= autoSwitchMin && score > bestScore {
				best = a
				bestScore = score
			}
		}
		if best == nil {
			return fmt.Errorf("no account found with quota >= %d%%", autoSwitchMin)
		}
		if autoSwitchJSON && !autoSwitchYes {
			return printJSON(map[string]any{
				"best_account": best.Email,
				"score":        bestScore,
				"target":       target.Label(tgt),
				"switched":     false,
			})
		}
		if !autoSwitchYes {
			fmt.Printf("Best account: %s (score %d%%, target %s)\n", best.Email, bestScore, target.Label(tgt))
			fmt.Print("Switch to this account? [y/N]: ")
			var ans string
			_, _ = fmt.Scanln(&ans)
			if strings.ToLower(strings.TrimSpace(ans)) != "y" {
				fmt.Println("Cancelled.")
				return nil
			}
		}
		if autoSwitchJSON {
			if err := switchAccountToTarget(best, tgt, true); err != nil {
				return err
			}
			return printJSON(map[string]any{
				"best_account": best.Email,
				"score":        bestScore,
				"target":       target.Label(tgt),
				"switched":     true,
			})
		}
		return switchAccountToTarget(best, tgt, false)
	},
}

func switchAccountToTarget(acc *db.Account, tgt target.Target, quiet bool) error {
	if acc.Token == nil {
		return fmt.Errorf("could not decrypt token for %s (run agm doctor)", acc.Email)
	}
	if db.IsTokenExpired(acc.Token) {
		if !quiet {
			fmt.Println("Token expired; refreshing…")
		}
		if _, _, err := api.ValidateAccount(acc); err != nil {
			return fmt.Errorf("refresh before switch: %w", err)
		}
		var err error
		acc, err = db.FindAccount(acc.Email)
		if err != nil {
			return err
		}
	}

	if !quiet {
		fmt.Printf("Switching %s → %s\n", acc.Email, target.Label(tgt))
	}
	var errs []string
	expanded := target.Expand(tgt)
	for _, t := range expanded {
		if err := switchOne(acc, t); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", target.Label(t), err))
			if !quiet {
				fmt.Printf("  ✗ %s: %v\n", target.Label(t), err)
			}
			continue
		}
		if !quiet {
			fmt.Printf("  ✓ %s\n", target.Label(t))
		}
		if acc.ID != "" {
			_ = db.SetActiveForTarget(string(t), acc.ID)
		}
	}
	_ = db.SetActive(acc.Email)
	if len(errs) > 0 && len(errs) == len(expanded) {
		return fmt.Errorf("switch failed: %s", strings.Join(errs, "; "))
	}
	if len(errs) > 0 {
		return fmt.Errorf("partial switch failure: %s", strings.Join(errs, "; "))
	}
	return nil
}

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Diagnostics",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("Running diagnostics...")

		type check struct {
			name string
			ok   bool
			warn bool
			msg  string
		}
		var checks []check

		_ = db.EnsureStore()

		checks = append(checks, check{"data_dir", true, false, paths.AgentDir()})
		checks = append(checks, check{"database", true, false, paths.CloudAccountsDBPath()})

		if src := crypto.KeySource(); src != "" {
			if _, err := crypto.LoadMasterKey(); err == nil {
				checks = append(checks, check{"master_key", true, false, src})
			} else {
				checks = append(checks, check{"master_key", false, false, err.Error()})
			}
		} else if _, err := crypto.EnsureMasterKey(); err == nil {
			checks = append(checks, check{"master_key", true, false, paths.MasterKeyPath()})
		} else {
			checks = append(checks, check{"master_key", false, false, err.Error()})
		}

		if p := paths.FindStateDB("ide"); p != "" {
			checks = append(checks, check{"ide_db", true, false, p})
		} else {
			checks = append(checks, check{"ide_db", false, true, "not found (needed for --target ide)"})
		}

		if p := process.FindAgyExecutable(); p != "" {
			checks = append(checks, check{"agy_exe", true, false, p})
		} else {
			checks = append(checks, check{"agy_exe", false, true, "not found (install Antigravity CLI)"})
		}

		if present, detail := credstore.Status(); present {
			checks = append(checks, check{"cli_creds", true, false, detail})
		} else {
			checks = append(checks, check{"cli_creds", false, true, detail})
		}

		if p := paths.FindExecutableForProduct("ide"); p != "" {
			checks = append(checks, check{"ide_exe", true, false, p})
		} else {
			checks = append(checks, check{"ide_exe", false, true, "not found"})
		}

		checks = append(checks, check{"quota_url", true, false, api.QuotaEndpoint()})

		accounts, err := db.ListAccounts()
		if err != nil {
			checks = append(checks, check{"accounts", false, false, err.Error()})
		} else if len(accounts) == 0 {
			checks = append(checks, check{"accounts", false, true, "0 — run agm login"})
		} else {
			checks = append(checks, check{"accounts", true, false, fmt.Sprintf("%d", len(accounts))})
		}

		for _, c := range checks {
			icon := "✓"
			if !c.ok && c.warn {
				icon = "⚠"
			} else if !c.ok {
				icon = "✗"
			}
			fmt.Printf("%s %s: %s\n", icon, strings.ToUpper(c.name), c.msg)
		}
		fmt.Println()
		return nil
	},
}

var watchCmd = &cobra.Command{
	Use:   "watch",
	Short: "Live list refresh",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Printf("Press Ctrl+C to stop. Refreshing every %ds...\n\n", watchInterval)
		for {
			fmt.Print("\033[H\033[2J")
			fmt.Printf("Press Ctrl+C to stop. Refreshing every %ds...\n\n", watchInterval)
			if err := runList(); err != nil {
				return err
			}
			time.Sleep(time.Duration(watchInterval) * time.Second)
		}
	},
}
