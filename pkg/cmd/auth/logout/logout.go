// Package logout implements `ado auth logout`.
package logout

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/snake-in-prod/ado-gh/internal/auth"
	"github.com/snake-in-prod/ado-gh/internal/config"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/auth/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// NewCmdLogout returns the `auth logout` command.
func NewCmdLogout(f *cmdutil.Factory) *cobra.Command {
	var org string
	cmd := &cobra.Command{
		Use:   "logout",
		Short: "Log out of an Azure DevOps organization",
		Long: `Remove the organization's stored PAT and, after a Microsoft Entra ID login, the
Entra ID account and its token cache. ADO_TOKEN and AZURE_DEVOPS_EXT_PAT are
not affected.

The organization comes from --org, else the current git remote, else ADO_ORG.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return logoutRun(cmd.Context(), f, org)
		},
	}
	cmd.Flags().StringVar(&org, "org", "", "Azure DevOps organization to log out of")
	return cmd
}

func logoutRun(ctx context.Context, f *cmdutil.Factory, orgFlag string) error {
	ios := f.IOStreams
	host, org := shared.OrgFromContext(orgFlag, f.BaseRepo)
	if org == "" {
		return errors.New(shared.NoOrg)
	}
	cfg, err := shared.StoreConfig(f)
	if err != nil {
		return err
	}
	store := &auth.Store{Config: cfg, ErrOut: ios.ErrOut}
	_, source := store.Token(host, org)
	id := cfg.Get(host, auth.HomeAccountIDKey)
	if source == "" && id == "" {
		return fmt.Errorf("not logged in to %s/%s", host, org)
	}
	if err := store.Delete(host, org); err != nil {
		return fmt.Errorf("delete token: %w", err)
	}
	if id != "" {
		if err := entraLogout(ctx, cfg, host, id, ios.ErrOut); err != nil {
			return err
		}
	}
	fmt.Fprintf(ios.ErrOut, "%s Logged out of %s/%s\n", ios.ColorScheme().Green("✓"), host, org)
	if source != "" && source != auth.SourceKeyring && source != auth.SourceConfig {
		fmt.Fprintf(ios.ErrOut, "! %s is set and still authenticates ado\n", source)
	}
	return nil
}

// entraLogout removes the MSAL account, then the token cache and its keyring key.
// ponytail: one cache serves every host, so this also signs out Entra ID on other hosts;
// keep the cache while another host has a home_account_id if that matters.
func entraLogout(ctx context.Context, cfg auth.Config, host, id string, errOut io.Writer) error {
	c := &auth.MSALCache{Dir: config.Dir(), ErrOut: errOut}
	// Best effort: the cache is deleted right after.
	if m, err := shared.MSAL(cfg, c); err == nil {
		if accounts, err := m.Accounts(ctx); err == nil {
			for _, a := range accounts {
				if a.HomeAccountID == id {
					_ = m.RemoveAccount(ctx, a)
				}
			}
		}
	}
	if err := c.Clear(ctx); err != nil {
		return fmt.Errorf("delete the Microsoft Entra ID token cache: %w", err)
	}
	cfg.Unset(host, auth.HomeAccountIDKey)
	return cfg.Write()
}
