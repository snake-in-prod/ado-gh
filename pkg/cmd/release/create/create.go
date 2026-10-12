// Package create implements `ado release create`.
package create

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/release"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	pshared "github.com/snake-in-prod/ado-gh/pkg/cmd/pipeline/shared"
	prshared "github.com/snake-in-prod/ado-gh/pkg/cmd/pr/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/release/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/release/watch"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// watchInterval is how often `create --watch` polls, as `release watch` does by default.
const watchInterval = 3 * time.Second

type options struct {
	definition, description string
	artifacts, vars         []string
	watch, web              bool
}

// NewCmdCreate returns the `release create` command.
func NewCmdCreate(f *cmdutil.Factory) *cobra.Command {
	o := &options{}
	cmd := &cobra.Command{
		Use:   "create <definition>",
		Short: "Create a release",
		Long: `Create a release of a Classic Release definition and print its URL.

<definition> is a release definition ID or name.

--artifact picks the version of an artifact by its alias; for a build artifact the
version is the build (run) ID. Artifacts not given use the version the definition
defaults to, and the versions chosen are printed.

--variable overrides a release variable; only variables the definition marks
"Settable at release time" can be set.

Environments with an automated trigger start deploying once the release is created.`,
		Example: `  ado release create ado-live-release
  ado release create 1 --artifact _ado-live-classic=3 -v greeting=hello --watch
  ado release create ado-live-release --description "hotfix" --web`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			o.definition = args[0]
			return runCreate(cmd.Context(), f, o)
		},
	}
	fl := cmd.Flags()
	fl.StringArrayVar(&o.artifacts, "artifact", nil, "Artifact version in `alias=version` format (default: the definition's default version)")
	fl.StringArrayVarP(&o.vars, "variable", "v", nil, "Release variable in `key=value` format (settable at release time only)")
	fl.StringVar(&o.description, "description", "", "Description of the release")
	fl.BoolVar(&o.watch, "watch", false, "Watch the release's deployments until they complete and exit with their result")
	fl.BoolVarP(&o.web, "web", "w", false, "Open the created release in the web browser")
	cmd.MarkFlagsMutuallyExclusive("watch", "web")
	return cmd
}

func runCreate(ctx context.Context, f *cmdutil.Factory, o *options) error {
	arts, err := pshared.ParsePairs("artifact", "alias=version", true, o.artifacts)
	if err != nil {
		return err
	}
	vars, err := pshared.ParsePairs("variable", "key=value", false, o.vars)
	if err != nil {
		return err
	}
	project, err := f.BaseProject()
	if err != nil {
		return err
	}
	c, err := shared.NewClient(ctx, f, project)
	if err != nil {
		return err
	}
	defID, err := shared.ResolveDefinition(ctx, c, o.definition)
	if err != nil {
		return err
	}
	def, err := c.ReleaseDefinition(ctx, defID)
	if err != nil {
		return err
	}
	draft := false
	meta := release.ReleaseStartMetadata{DefinitionId: &defID, IsDraft: &draft}
	if o.description != "" {
		meta.Description = &o.description
	}
	if len(arts) > 0 {
		if meta.Artifacts, err = artifactMetadata(def, arts); err != nil {
			return err
		}
	}
	if len(vars) > 0 {
		if meta.Variables, err = overrides(def, vars); err != nil {
			return err
		}
	}
	r, err := c.CreateRelease(ctx, meta)
	if err != nil {
		return err
	}

	id := prshared.Deref(r.Id)
	ios := f.IOStreams
	cs := ios.ColorScheme()
	if ios.IsStdoutTTY() {
		fmt.Fprintf(ios.ErrOut, "%s Created %s of %s\n", cs.Green("✓"), prshared.Deref(r.Name), prshared.Deref(def.Name))
	}
	// The versions are printed whether or not --artifact chose them: unset ones are the server's pick.
	for _, a := range prshared.Deref(r.Artifacts) {
		v := prshared.Deref(a.DefinitionReference)["version"]
		fmt.Fprintf(ios.ErrOut, "  %s: %s\n", prshared.Deref(a.Alias), cmp.Or(prshared.Deref(v.Name), prshared.Deref(v.Id), "(none)"))
	}
	url := adocontext.ReleaseURL(project, id)
	if o.web {
		return cmdutil.OpenInBrowser(f, url)
	}
	fmt.Fprintln(ios.Out, url)
	if !o.watch {
		if ios.IsStdoutTTY() {
			fmt.Fprintf(ios.ErrOut, "\nTo see the release's progress, try: ado release watch %d\n", id)
		}
		return nil
	}
	return watch.Release(ctx, ios, c, project, id, "", watchInterval)
}

// artifactMetadata returns the artifact versions arts (alias → version) for def, matching
// aliases without regard to case; an alias def lacks is an error listing those it has.
func artifactMetadata(def *release.ReleaseDefinition, arts map[string]string) (*[]release.ArtifactMetadata, error) {
	var aliases []string
	for _, a := range prshared.Deref(def.Artifacts) {
		aliases = append(aliases, prshared.Deref(a.Alias))
	}
	out := make([]release.ArtifactMetadata, 0, len(arts))
	for _, k := range slices.Sorted(maps.Keys(arts)) {
		i := slices.IndexFunc(aliases, func(a string) bool { return strings.EqualFold(a, k) })
		if i < 0 {
			return nil, fmt.Errorf("release definition %q has no artifact %q; its artifacts: %s", prshared.Deref(def.Name), k, listOrNone(aliases))
		}
		v := arts[k]
		out = append(out, release.ArtifactMetadata{Alias: &aliases[i], InstanceReference: &release.BuildVersion{Id: &v}})
	}
	return &out, nil
}

// overrides returns the release variable values vars (name → value) for def, matching names
// without regard to case; a variable def lacks or doesn't allow to override is an error.
func overrides(def *release.ReleaseDefinition, vars map[string]string) (*map[string]release.ConfigurationVariableValue, error) {
	defVars := prshared.Deref(def.Variables)
	names := slices.Sorted(maps.Keys(defVars))
	var settable []string
	for _, n := range names {
		if prshared.Deref(defVars[n].AllowOverride) {
			settable = append(settable, n)
		}
	}
	out := make(map[string]release.ConfigurationVariableValue, len(vars))
	for _, k := range slices.Sorted(maps.Keys(vars)) {
		i := slices.IndexFunc(names, func(n string) bool { return strings.EqualFold(n, k) })
		if i < 0 {
			return nil, fmt.Errorf("release definition %q has no release variable %q; settable at release time: %s", prshared.Deref(def.Name), k, listOrNone(settable))
		}
		if !prshared.Deref(defVars[names[i]].AllowOverride) {
			return nil, fmt.Errorf("variable %q of release definition %q is not settable at release time; settable: %s", names[i], prshared.Deref(def.Name), listOrNone(settable))
		}
		v := vars[k]
		out[names[i]] = release.ConfigurationVariableValue{Value: &v}
	}
	return &out, nil
}

func listOrNone(s []string) string {
	if len(s) == 0 {
		return "none"
	}
	return strings.Join(s, ", ")
}
