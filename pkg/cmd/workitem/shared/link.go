package shared

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/snake-in-prod/ado-gh/api"
)

const hyperlinkRel = "Hyperlink"

// Link is a relation to add to or remove from a work item.
type Link struct {
	Rel string
	URL string
	// ID is the linked work item's ID; 0 for a hyperlink.
	ID int
}

// ParseLink parses a `<type>:<target>` link argument. type is a work item link type by friendly
// or reference name, any case (parent, child, related, "Tested By", ...), with a work item
// reference as target, or hyperlink/url with a URL as target. types are the organization's
// relation types; work item URLs are built on orgURL (ending in "/").
func ParseLink(arg string, types []api.RelationType, orgURL string) (Link, error) {
	name, target, ok := strings.Cut(arg, ":")
	name, target = strings.TrimSpace(name), strings.TrimSpace(target)
	if !ok || name == "" || target == "" {
		return Link{}, fmt.Errorf("invalid link %q: expected <type>:<target>, e.g. related:123", arg)
	}
	if strings.EqualFold(name, "url") || strings.EqualFold(name, hyperlinkRel) {
		if !strings.Contains(target, "://") {
			return Link{}, fmt.Errorf("invalid link %q: a hyperlink needs a URL", arg)
		}
		return Link{Rel: hyperlinkRel, URL: target}, nil
	}
	i := slices.IndexFunc(types, func(t api.RelationType) bool {
		return t.Usage == "workItemLink" && (strings.EqualFold(t.Name, name) || strings.EqualFold(t.ReferenceName, name))
	})
	if i < 0 {
		var names []string
		for _, t := range types {
			if t.Usage == "workItemLink" {
				names = append(names, t.Name)
			}
		}
		return Link{}, fmt.Errorf("unknown link type %q; valid types: %s, url", name, strings.Join(names, ", "))
	}
	id, _, err := ParseRef(target)
	if err != nil {
		return Link{}, err
	}
	return Link{Rel: types[i].ReferenceName, URL: orgURL + "_apis/wit/workItems/" + strconv.Itoa(id), ID: id}, nil
}

// Matches reports whether r is this link: the same type and, for a work item link, a URL
// ending in /workItems/{ID} (hosts and casing differ), else the same URL.
func (l Link) Matches(r api.WorkItemRelation) bool {
	if !strings.EqualFold(r.Rel, l.Rel) {
		return false
	}
	if l.ID > 0 {
		return strings.HasSuffix(strings.ToLower(r.URL), "/workitems/"+strconv.Itoa(l.ID))
	}
	return strings.EqualFold(r.URL, l.URL)
}

// String is the link as `<type>:<target>`.
func (l Link) String() string {
	if l.ID > 0 {
		return l.Rel + ":" + strconv.Itoa(l.ID)
	}
	return l.Rel + ":" + l.URL
}
