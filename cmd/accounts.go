package cmd

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/shyim/agm/internal/aliases"
	"github.com/shyim/agm/internal/api"
	"github.com/shyim/agm/internal/credstore"
	"github.com/shyim/agm/internal/db"
	"github.com/shyim/agm/internal/paths"
	"github.com/shyim/agm/internal/process"
	"github.com/shyim/agm/internal/target"
	"github.com/spf13/cobra"
)

var (
	switchTarget string
	listJSON     bool
	infoJSON     bool
	statusJSON   bool
)

func init() {
	listCmd.Flags().BoolVar(&listJSON, "json", false, "Output JSON")
	infoCmd.Flags().BoolVar(&infoJSON, "json", false, "Output JSON")
	statusCmd.Flags().BoolVar(&statusJSON, "json", false, "Output JSON")

	RootCmd.AddCommand(listCmd)
	RootCmd.AddCommand(infoCmd)
	RootCmd.AddCommand(statusCmd)

	switchCmd.Flags().StringVarP(&switchTarget, "target", "t", "all", "Apply account to target product(s) (agy|ide|all)")
	_ = switchCmd.RegisterFlagCompletionFunc("target", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return []string{"all", "ide", "agy"}, cobra.ShellCompDirectiveNoFileComp
	})
	RootCmd.AddCommand(switchCmd)

	RootCmd.AddCommand(refreshCmd)
	RootCmd.AddCommand(refreshAllCmd)
	RootCmd.AddCommand(validateCmd)
	RootCmd.AddCommand(syncCmd)
	RootCmd.AddCommand(removeCmd)
}

func runList() error {
	accounts, err := db.ListAccounts()
	if err != nil {
		return err
	}
	activeAgy := db.GetActiveForTarget(string(target.Agy))
	activeIDE := db.GetActiveForTarget(string(target.IDE))
	now := time.Now()

	if listJSON {
		type jsonRow struct {
			Email     string   `json:"email"`
			Status    []string `json:"status"`
			GemPro    string   `json:"gem_pro"`
			GemFlash  string   `json:"gem_flash"`
			Claude    string   `json:"claude"`
			NextReset string   `json:"next_reset"`
		}
		out := make([]jsonRow, 0, len(accounts))
		for _, a := range accounts {
			gp, gf, cl := quotaGroups(a.Quota)
			out = append(out, jsonRow{
				Email:     a.Email,
				Status:    accountStatusTags(a, activeAgy, activeIDE),
				GemPro:    gp,
				GemFlash:  gf,
				Claude:    cl,
				NextReset: nextResetSummary(a.Quota, now),
			})
		}
		return printJSON(out)
	}

	if len(accounts) == 0 {
		fmt.Println("No accounts yet.")
		fmt.Println("  agm login        — browser OAuth")
		fmt.Println("  agm import-ide   — pull from signed-in Antigravity IDE")
		return nil
	}

	fmt.Printf("%-36s %-14s %9s %9s %9s  %s\n", "EMAIL", "STATUS", "GEM-PRO", "GEM-FLASH", "CLAUDE", "NEXT-RESET")
	fmt.Println(strings.Repeat("-", 96))
	for _, a := range accounts {
		status := strings.Join(accountStatusTags(a, activeAgy, activeIDE), ",")
		gp, gf, cl := quotaGroups(a.Quota)
		rst := nextResetSummary(a.Quota, now)
		fmt.Printf("%-36s %-14s %9s %9s %9s  %s\n", a.Email, status, gp, gf, cl, rst)
	}
	return nil
}

func accountStatusTags(a db.Account, activeAgy, activeIDE string) []string {
	var tags []string
	if a.ID != "" && a.ID == activeAgy {
		tags = append(tags, "cli")
	}
	if a.ID != "" && a.ID == activeIDE {
		tags = append(tags, "ide")
	}
	if a.IsActive && len(tags) == 0 {
		tags = append(tags, "active")
	}
	if db.IsTokenExpired(a.Token) {
		tags = append(tags, "token-exp")
	}
	return tags
}

func printJSON(v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(raw))
	return nil
}

func formatDurationShort(d time.Duration) string {
	if d <= 0 {
		return "now"
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if h > 0 {
		return fmt.Sprintf("in %dh %dm", h, m)
	}
	return fmt.Sprintf("in %dm", m)
}

func formatResetTime(raw string, now time.Time) string {
	if raw == "" {
		return "-"
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return raw
	}
	local := t.Local()
	return fmt.Sprintf("%s (%s)", local.Format("3:04 PM"), formatDurationShort(t.Sub(now)))
}

func nextResetSummary(q *db.Quota, now time.Time) string {
	if q == nil || len(q.Models) == 0 {
		return "-"
	}
	var earliest *time.Time
	for _, m := range q.Models {
		if m.ResetTime == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339, m.ResetTime)
		if err != nil || !t.After(now) {
			continue
		}
		if earliest == nil || t.Before(*earliest) {
			tt := t
			earliest = &tt
		}
	}
	if earliest == nil {
		return "-"
	}
	return formatDurationShort(earliest.Sub(now))
}

var listCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List accounts with quota summary",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runList()
	},
}

func quotaGroups(q *db.Quota) (gp, gf, cl string) {
	if q == nil || len(q.Models) == 0 {
		return "-", "-", "-"
	}
	var gpV, gfV, clV []int
	for name, m := range q.Models {
		n := strings.ToLower(name)
		if strings.Contains(n, "gemini") && strings.Contains(n, "pro") {
			gpV = append(gpV, m.Percentage)
		}
		if strings.Contains(n, "gemini") && strings.Contains(n, "flash") {
			gfV = append(gfV, m.Percentage)
		}
		if strings.Contains(n, "claude") {
			clV = append(clV, m.Percentage)
		}
	}
	return minPct(gpV), minPct(gfV), minPct(clV)
}

func minPct(vals []int) string {
	if len(vals) == 0 {
		return "-"
	}
	m := vals[0]
	for _, v := range vals[1:] {
		if v < m {
			m = v
		}
	}
	return fmt.Sprintf("%d%%", m)
}

var infoCmd = &cobra.Command{
	Use:               "info <email|alias>",
	Short:             "Detailed quotas for one account",
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeAccountsAndAliases,
	RunE: func(cmd *cobra.Command, args []string) error {
		acc, err := db.FindAccount(resolve(args[0]))
		if err != nil {
			return err
		}
		if infoJSON {
			type modelRow struct {
				Provider  string `json:"provider"`
				Model     string `json:"model"`
				Score     int    `json:"score"`
				ResetTime string `json:"reset_time,omitempty"`
			}
			type infoPayload struct {
				Email        string     `json:"email"`
				TokenExpiry  int64      `json:"token_expiry,omitempty"`
				TokenExpired bool       `json:"token_expired"`
				Models       []modelRow `json:"models"`
			}
			out := infoPayload{
				Email:        acc.Email,
				TokenExpired: db.IsTokenExpired(acc.Token),
				Models:       []modelRow{},
			}
			if acc.Token != nil {
				out.TokenExpiry = acc.Token.ExpiryTimestamp
			}
			if acc.Quota != nil {
				for name, m := range acc.Quota.Models {
					out.Models = append(out.Models, modelRow{
						Provider:  modelProvider(name),
						Model:     strings.TrimPrefix(name, "cloudaicompanion.googleapis.com/"),
						Score:     m.Percentage,
						ResetTime: m.ResetTime,
					})
				}
				sort.Slice(out.Models, func(i, j int) bool {
					if out.Models[i].Score != out.Models[j].Score {
						return out.Models[i].Score > out.Models[j].Score
					}
					if out.Models[i].Provider != out.Models[j].Provider {
						return out.Models[i].Provider < out.Models[j].Provider
					}
					return out.Models[i].Model < out.Models[j].Model
				})
			}
			return printJSON(out)
		}

		fmt.Printf("Account: %s\n", acc.Email)
		if acc.Token != nil {
			exp := "unknown"
			if acc.Token.ExpiryTimestamp > 0 {
				exp = time.Unix(acc.Token.ExpiryTimestamp, 0).Local().Format(time.RFC3339)
			}
			fmt.Printf("Token expiry: %s", exp)
			if db.IsTokenExpired(acc.Token) {
				fmt.Print(" (expired)")
			}
			fmt.Println()
		}
		if acc.Quota == nil || len(acc.Quota.Models) == 0 {
			fmt.Println("No quota data. Run: agm refresh " + acc.Email)
			return nil
		}
		fmt.Printf("\n%-12s %-48s %6s  %s\n", "PROVIDER", "MODEL", "SCORE", "RESET")
		fmt.Println(strings.Repeat("-", 96))
		type row struct {
			provider string
			name     string
			pct      int
			rst      string
		}
		var rows []row
		for name, m := range acc.Quota.Models {
			rows = append(rows, row{
				provider: modelProvider(name),
				name:     strings.TrimPrefix(name, "cloudaicompanion.googleapis.com/"),
				pct:      m.Percentage,
				rst:      m.ResetTime,
			})
		}
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].pct != rows[j].pct {
				return rows[i].pct > rows[j].pct
			}
			if rows[i].provider != rows[j].provider {
				return rows[i].provider < rows[j].provider
			}
			return rows[i].name < rows[j].name
		})
		now := time.Now()
		for _, r := range rows {
			fmt.Printf("%-12s %-48s %5d%%  %s\n", r.provider, r.name, r.pct, formatResetTime(r.rst, now))
		}
		return nil
	},
}

func modelProvider(name string) string {
	ln := strings.ToLower(name)
	if strings.Contains(ln, "gemini") {
		return "GOOGLE"
	}
	if strings.Contains(ln, "claude") {
		return "ANTHROPIC"
	}
	return "OTHER"
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Active account quick view",
	RunE: func(cmd *cobra.Command, args []string) error {
		accounts, err := db.ListAccounts()
		if err != nil {
			return err
		}
		var active *db.Account
		for i := range accounts {
			if accounts[i].IsActive {
				active = &accounts[i]
				break
			}
		}
		if active == nil && len(accounts) > 0 {
			active = &accounts[0]
		}
		if active == nil {
			if statusJSON {
				return printJSON(map[string]any{"active": nil})
			}
			fmt.Println("No accounts. Run: agm login")
			return nil
		}
		gp, gf, cl := quotaGroups(active.Quota)
		rst := nextResetSummary(active.Quota, time.Now())
		if statusJSON {
			return printJSON(map[string]any{
				"email":      active.Email,
				"gem_pro":    gp,
				"gem_flash":  gf,
				"claude":     cl,
				"next_reset": rst,
			})
		}
		fmt.Printf("Active Account: %s\n", active.Email)
		fmt.Printf("Gemini Pro: %s\n", gp)
		fmt.Printf("Gemini Fl:  %s\n", gf)
		fmt.Printf("Claude:     %s\n", cl)
		fmt.Printf("Next Reset: %s\n", rst)
		return nil
	},
}

var switchCmd = &cobra.Command{
	Use:               "switch <email|alias>",
	Short:             "Apply account to target product(s)",
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeAccountsAndAliases,
	RunE: func(cmd *cobra.Command, args []string) error {
		tgt, err := target.Parse(switchTarget)
		if err != nil {
			return err
		}
		pattern := args[0]

		acc, err := db.FindAccount(resolve(pattern))
		if err != nil {
			return err
		}
		if acc.Token == nil {
			return fmt.Errorf("could not decrypt token for %s (run agm doctor)", acc.Email)
		}
		if db.IsTokenExpired(acc.Token) {
			fmt.Println("Token expired; refreshing…")
			if _, _, err := api.ValidateAccount(acc); err != nil {
				return fmt.Errorf("refresh before switch: %w", err)
			}
			acc, err = db.FindAccount(acc.Email)
			if err != nil {
				return err
			}
		}

		fmt.Printf("Switching %s → %s\n", acc.Email, target.Label(tgt))
		var errs []string
		for _, t := range target.Expand(tgt) {
			if err := switchOne(acc, t); err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", target.Label(t), err))
				fmt.Printf("  ✗ %s: %v\n", target.Label(t), err)
				continue
			}
			fmt.Printf("  ✓ %s\n", target.Label(t))
			if acc.ID != "" {
				_ = db.SetActiveForTarget(string(t), acc.ID)
			}
		}
		_ = db.SetActive(acc.Email)
		if len(errs) > 0 && len(errs) == len(target.Expand(tgt)) {
			return fmt.Errorf("switch failed: %s", strings.Join(errs, "; "))
		}
		if len(errs) > 0 {
			return fmt.Errorf("partial switch failure: %s", strings.Join(errs, "; "))
		}
		return nil
	},
}

func switchOne(acc *db.Account, t target.Target) error {
	return process.SwitchFlow(process.SwitchOptions{
		Target: t,
		Inject: func() error {
			if target.UsesCredentialStore(t) {
				if err := credstore.WriteToken(acc.Token); err != nil {
					return fmt.Errorf("credential store: %w", err)
				}
			}
			if target.UsesSQLiteInject(t) {
				if err := db.InjectTokenIntoStateDB(acc, "ide"); err != nil {
					return err
				}
			}
			return nil
		},
	})
}

var refreshCmd = &cobra.Command{
	Use:               "refresh <email|alias>",
	Short:             "Refresh live quotas for one account",
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeAccountsAndAliases,
	RunE: func(cmd *cobra.Command, args []string) error {
		acc, err := db.FindAccount(resolve(args[0]))
		if err != nil {
			return err
		}
		fmt.Printf("Refreshing quota for %s...\n", acc.Email)
		if err := api.RefreshAccountQuota(acc); err != nil {
			return err
		}
		fmt.Println("Quota updated successfully.")
		return nil
	},
}

var refreshAllCmd = &cobra.Command{
	Use:   "refresh-all",
	Short: "Refresh quotas for all accounts",
	RunE: func(cmd *cobra.Command, args []string) error {
		accounts, err := db.ListAccounts()
		if err != nil {
			return err
		}
		if len(accounts) == 0 {
			fmt.Println("No accounts. Run: agm login")
			return nil
		}
		fmt.Printf("Starting bulk refresh for %d accounts...\n\n", len(accounts))
		ok, fail := 0, 0
		for i := range accounts {
			fmt.Printf("→ %s\n", accounts[i].Email)
			if err := api.RefreshAccountQuota(&accounts[i]); err != nil {
				fmt.Printf("  ✗ %v\n", err)
				fail++
				continue
			}
			fmt.Println("  ✓ ok")
			ok++
		}
		fmt.Printf("\nCompleted: %d successful", ok)
		if fail > 0 {
			fmt.Printf(", %d failed", fail)
		}
		fmt.Println()
		return nil
	},
}

var validateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Refresh expired tokens",
	RunE: func(cmd *cobra.Command, args []string) error {
		accounts, err := db.ListAccounts()
		if err != nil {
			return err
		}
		fmt.Println("Validating all account tokens...")
		valid, refreshed, errors := 0, 0, 0
		for i := range accounts {
			v, r, err := api.ValidateAccount(&accounts[i])
			if err != nil {
				fmt.Printf("✗ %s: %v\n", accounts[i].Email, err)
				errors++
				continue
			}
			if r {
				fmt.Printf("✓ %s: Token refreshed\n", accounts[i].Email)
				refreshed++
			} else if v {
				fmt.Printf("✓ %s: Token valid\n", accounts[i].Email)
				valid++
			}
		}
		fmt.Printf("\nSummary:\n  Valid: %d\n  Refreshed: %d\n", valid, refreshed)
		if errors > 0 {
			fmt.Printf("  Errors: %d\n", errors)
		}
		return nil
	},
}

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Compare local DB vs IDE / CLI credential store",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("Account Sync Status")
		fmt.Printf("  Local DB: %s\n", paths.CloudAccountsDBPath())
		if ide := paths.FindStateDB("ide"); ide != "" {
			fmt.Printf("  IDE DB:   %s\n", ide)
		} else {
			fmt.Println("  IDE DB:   (not found)")
		}
		if exe := process.FindAgyExecutable(); exe != "" {
			fmt.Printf("  agy CLI:  %s\n", exe)
		} else {
			fmt.Println("  agy CLI:  (not found on PATH)")
		}
		fmt.Println()

		accounts, err := db.ListAccounts()
		if err != nil {
			return err
		}
		cliSet := map[string]*db.Account{}
		if len(accounts) == 0 {
			fmt.Println("Local accounts: (none) — agm login")
		} else {
			fmt.Println("Local accounts:")
			for i := range accounts {
				a := &accounts[i]
				cliSet[a.Email] = a
				marks := []string{}
				if a.ID == db.GetActiveForTarget(string(target.Agy)) {
					marks = append(marks, "cli")
				}
				if a.ID == db.GetActiveForTarget(string(target.IDE)) {
					marks = append(marks, "ide")
				}
				extra := ""
				if len(marks) > 0 {
					extra = " [" + strings.Join(marks, ",") + "]"
				}
				fmt.Printf("  • %s%s\n", a.Email, extra)
			}
		}

		// IDE
		if ideEmail, ideErr := db.GetIDEActiveEmail(); ideErr != nil {
			fmt.Printf("\nIDE active: unavailable (%v)\n", ideErr)
		} else if ideEmail == "" {
			fmt.Println("\nIDE active: (none)")
		} else {
			fmt.Printf("\nIDE active: %s\n", ideEmail)
			if _, ok := cliSet[ideEmail]; ok {
				fmt.Println("  Present in local DB.")
			} else {
				fmt.Println("  Not in local DB — run: agm import-ide")
			}
		}

		// CLI credential store
		if tok, err := credstore.ReadToken(); err != nil {
			fmt.Printf("\nCLI (agy) credential store: unavailable (%v)\n", err)
		} else {
			email := ""
			if ui, err := api.FetchUserInfo(tok.AccessToken); err == nil && ui != nil {
				email = ui.Email
			}
			if email == "" {
				// match local by refresh token
				for _, a := range accounts {
					if a.Token != nil && a.Token.RefreshToken == tok.RefreshToken {
						email = a.Email
						break
					}
				}
			}
			if email == "" {
				fmt.Println("\nCLI (agy) credential store: present (email unknown)")
				fmt.Println("  Run: agm import-cli")
			} else {
				fmt.Printf("\nCLI (agy) credential store: %s\n", email)
				if _, ok := cliSet[email]; ok {
					fmt.Println("  Present in local DB.")
				} else {
					fmt.Println("  Not in local DB — run: agm import-cli")
				}
			}
		}
		return nil
	},
}

var removeCmd = &cobra.Command{
	Use:               "remove <email|alias>",
	Aliases:           []string{"rm"},
	Short:             "Delete account from local DB",
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeAccountsAndAliases,
	RunE: func(cmd *cobra.Command, args []string) error {
		acc, err := db.FindAccount(resolve(args[0]))
		if err != nil {
			return err
		}
		fmt.Printf("Remove %s from local database? [y/N]: ", acc.Email)
		var ans string
		_, _ = fmt.Scanln(&ans)
		if strings.ToLower(strings.TrimSpace(ans)) != "y" {
			fmt.Println("Cancelled.")
			return nil
		}
		if err := db.RemoveAccount(acc.Email); err != nil {
			return err
		}
		fmt.Printf("Removed %s\n", acc.Email)
		return nil
	},
}

func completeAccountsAndAliases(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
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
	for alias, email := range aliases.Load() {
		completions = append(completions, alias+"\t"+"Alias for "+email)
	}
	return completions, cobra.ShellCompDirectiveNoFileComp
}
