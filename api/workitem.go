package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/core"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/webapi"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/work"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/workitemtracking"
)

// workItemLocation is the WIT workItems resource. WorkItem reads it through SDK.Send because
// the SDK's WorkItem model drops multilineFieldsFormat.
var workItemLocation = uuid.MustParse("72c7ddf8-2cdc-4f60-90cd-ab71c14a399b")

const (
	// workItemBatchSize is the workitemsbatch limit on IDs per request.
	workItemBatchSize = 200
	// updatesPageSize is the $top sent per work item updates page.
	updatesPageSize = 200
)

// WorkItemClient calls the Azure DevOps work item tracking, work (teams, iterations, boards)
// and core APIs for one project.
type WorkItemClient struct {
	Wit  workitemtracking.Client
	Work work.Client
	Core core.Client
	// SDK sends the requests the typed clients have no method or model for.
	SDK *azuredevops.Client
	// Project is the project name or ID.
	Project string
}

// NewWorkItemClient returns a WorkItemClient for project in the organization at orgURL,
// sending requests through hc.
func NewWorkItemClient(hc *http.Client, orgURL, project string) *WorkItemClient {
	sdk := NewSDKClient(hc, orgURL)
	return &WorkItemClient{
		Wit: &workitemtracking.ClientImpl{Client: *sdk}, Work: &work.ClientImpl{Client: *sdk},
		Core: &core.ClientImpl{Client: *sdk}, SDK: sdk, Project: project,
	}
}

// WorkItem is a work item as the WIT API returns it.
type WorkItem struct {
	ID  int `json:"id"`
	Rev int `json:"rev"`
	// Fields maps reference names to values; identity fields are objects.
	Fields    map[string]any     `json:"fields"`
	Relations []WorkItemRelation `json:"relations"`
	URL       string             `json:"url"`
	// MultilineFieldsFormat maps a large-text field's reference name to its format
	// ("Markdown" or "Html", any case); a missing field is HTML. Only WorkItem fills it.
	MultilineFieldsFormat map[string]string `json:"multilineFieldsFormat"`
}

// WorkItemRelation is a link from a work item to another work item, an artifact or a URL.
type WorkItemRelation struct {
	Rel        string         `json:"rel"`
	URL        string         `json:"url"`
	Attributes map[string]any `json:"attributes"`
}

// IdentityRef is the user behind a comment or update.
type IdentityRef struct {
	ID          string
	DisplayName string
	// UniqueName is usually the email or UPN.
	UniqueName string
}

// WorkItemComment is one comment of a work item.
type WorkItemComment struct {
	ID      int
	Version int
	Text    string
	// Format is "markdown" or "html".
	Format       string
	CreatedBy    IdentityRef
	CreatedDate  time.Time
	ModifiedDate time.Time
	IsDeleted    bool
	URL          string
}

// WorkItemUpdate is one revision of a work item's change log.
type WorkItemUpdate struct {
	ID               int
	Rev              int
	RevisedBy        IdentityRef
	RevisedDate      time.Time
	Fields           map[string]FieldChange
	RelationsAdded   []WorkItemRelation
	RelationsRemoved []WorkItemRelation
}

// FieldChange is a field's value before and after an update.
type FieldChange struct {
	OldValue any
	NewValue any
}

// RelationType is a work item link type or resource link type.
type RelationType struct {
	Name          string
	ReferenceName string
	// Usage is "workItemLink" or "resourceLink".
	Usage   string
	Enabled bool
}

// QueryItem is a saved query or query folder.
type QueryItem struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Path     string `json:"path"`
	IsFolder bool   `json:"isFolder"`
	// QueryType is "flat", "tree" or "oneHop"; "" for a folder.
	QueryType   string      `json:"queryType"`
	IsPublic    bool        `json:"isPublic"`
	HasChildren bool        `json:"hasChildren"`
	Wiql        string      `json:"wiql"`
	Children    []QueryItem `json:"children"`
}

// QueryResult is the result of a WIQL or saved query: IDs for a flat query, Links for a tree
// or one-hop query.
type QueryResult struct {
	QueryType string
	Columns   []QueryColumn
	IDs       []int
	Links     []WorkItemLink
}

// QueryColumn is a query column: a field's reference name and display name.
type QueryColumn struct {
	ReferenceName string
	Name          string
}

// WorkItemLink is one row of a link query; SourceID 0 marks a root.
type WorkItemLink struct {
	Rel      string
	SourceID int
	TargetID int
}

// ClassificationNode is an area or iteration node.
type ClassificationNode struct {
	ID int
	// Name is the node's last path segment.
	Name string
	// Path is the backslash-separated path, e.g. \Project\Iteration\Sprint 1.
	Path        string
	HasChildren bool
	Children    []ClassificationNode
}

// IsRevConflict reports whether err is a failed `test /rev` patch op, i.e. the work item
// changed since it was read: TypeKey TestPatchOperationFailedException, else a VS403351
// message or a 412 status.
func IsRevConflict(err error) bool {
	var he *HTTPError
	return errors.As(err, &he) && (he.TypeKey == "TestPatchOperationFailedException" ||
		strings.Contains(he.Message, "VS403351") || he.StatusCode == http.StatusPreconditionFailed)
}

// WorkItem fetches work item id with all its fields, and its relations when expandRelations.
func (c *WorkItemClient) WorkItem(ctx context.Context, id int, expandRelations bool) (*WorkItem, error) {
	q := url.Values{}
	if expandRelations {
		q.Set("$expand", string(workitemtracking.WorkItemExpandValues.Relations))
	}
	resp, err := c.SDK.Send(ctx, http.MethodGet, workItemLocation, "7.1-preview.3",
		map[string]string{"project": c.Project, "id": strconv.Itoa(id)}, q, nil, "", "application/json", nil)
	if err != nil {
		return nil, mapErr(err)
	}
	defer resp.Body.Close()
	var wi WorkItem
	if err := c.SDK.UnmarshalBody(resp, &wi); err != nil {
		return nil, err
	}
	return &wi, nil
}

// WorkItems fetches the work items ids in that order, workItemBatchSize per request. Deleted
// or inaccessible items are skipped. fields limits the fields returned (nil means all);
// expandRelations adds relations, and then fields is ignored, as the API rejects both.
func (c *WorkItemClient) WorkItems(ctx context.Context, ids []int, fields []string, expandRelations bool) ([]WorkItem, error) {
	byID := make(map[int]WorkItem, len(ids))
	for chunk := range slices.Chunk(ids, workItemBatchSize) {
		req := workitemtracking.WorkItemBatchGetRequest{Ids: &chunk, ErrorPolicy: &workitemtracking.WorkItemErrorPolicyValues.Omit}
		if expandRelations {
			req.Expand = &workitemtracking.WorkItemExpandValues.Relations
		} else if len(fields) > 0 {
			req.Fields = &fields
		}
		items, err := c.Wit.GetWorkItemsBatch(ctx, workitemtracking.GetWorkItemsBatchArgs{WorkItemGetRequest: &req, Project: &c.Project})
		if err != nil {
			return nil, mapErr(err)
		}
		for _, w := range deref(items) {
			// errorPolicy=Omit answers null for an omitted item, decoded without an Id.
			if w.Id != nil {
				byID[*w.Id] = toWorkItem(w)
			}
		}
	}
	out := make([]WorkItem, 0, len(byID))
	for _, id := range ids {
		if wi, ok := byID[id]; ok {
			out = append(out, wi)
		}
	}
	return out, nil
}

// CreateWorkItem creates a work item of type typ from the JSON Patch ops.
func (c *WorkItemClient) CreateWorkItem(ctx context.Context, typ string, ops []webapi.JsonPatchOperation) (*WorkItem, error) {
	w, err := c.Wit.CreateWorkItem(ctx, workitemtracking.CreateWorkItemArgs{Document: &ops, Project: &c.Project, Type: &typ})
	if err != nil {
		return nil, mapErr(err)
	}
	wi := toWorkItem(*w)
	return &wi, nil
}

// UpdateWorkItem applies the JSON Patch ops to work item id. Check a `test /rev` failure with
// IsRevConflict.
func (c *WorkItemClient) UpdateWorkItem(ctx context.Context, id int, ops []webapi.JsonPatchOperation) (*WorkItem, error) {
	w, err := c.Wit.UpdateWorkItem(ctx, workitemtracking.UpdateWorkItemArgs{Document: &ops, Id: &id, Project: &c.Project})
	if err != nil {
		return nil, mapErr(err)
	}
	wi := toWorkItem(*w)
	return &wi, nil
}

// DeleteWorkItem moves work item id to the recycle bin, or destroys it for good when destroy.
// It sends the SDK's DeleteWorkItem request itself: destroy answers 204 with no body, which
// the SDK fails to decode.
func (c *WorkItemClient) DeleteWorkItem(ctx context.Context, id int, destroy bool) error {
	q := url.Values{}
	if destroy {
		q.Set("destroy", "true")
	}
	resp, err := c.SDK.Send(ctx, http.MethodDelete, workItemLocation, "7.1-preview.3",
		map[string]string{"project": c.Project, "id": strconv.Itoa(id)}, q, nil, "", "application/json", nil)
	if err != nil {
		return mapErr(err)
	}
	return resp.Body.Close()
}

// RestoreWorkItem brings work item id back from the recycle bin and returns its title. An item
// not in the recycle bin answers 404.
func (c *WorkItemClient) RestoreWorkItem(ctx context.Context, id int) (string, error) {
	deleted := false
	d, err := c.Wit.RestoreWorkItem(ctx, workitemtracking.RestoreWorkItemArgs{
		Payload: &workitemtracking.WorkItemDeleteUpdate{IsDeleted: &deleted}, Id: &id, Project: &c.Project,
	})
	if err != nil {
		return "", mapErr(err)
	}
	return deref(d.Name), nil
}

// Comments returns one page of work item id's comments, newest first when desc, and the
// continuation token of the next page ("" on the last). Pass "" for the first page.
func (c *WorkItemClient) Comments(ctx context.Context, id int, desc bool, continuationToken string) ([]WorkItemComment, string, error) {
	order := workitemtracking.CommentSortOrderValues.Asc
	if desc {
		order = workitemtracking.CommentSortOrderValues.Desc
	}
	args := workitemtracking.GetCommentsArgs{Project: &c.Project, WorkItemId: &id, Order: &order}
	if continuationToken != "" {
		args.ContinuationToken = &continuationToken
	}
	list, err := c.Wit.GetComments(ctx, args)
	if err != nil {
		return nil, "", mapErr(err)
	}
	var out []WorkItemComment
	for _, cm := range deref(list.Comments) {
		out = append(out, toComment(cm))
	}
	return out, deref(list.ContinuationToken), nil
}

// AddComment adds a comment to work item id, in Markdown when markdown, else HTML.
func (c *WorkItemClient) AddComment(ctx context.Context, id int, text string, markdown bool) (*WorkItemComment, error) {
	cm, err := c.Wit.AddWorkItemComment(ctx, workitemtracking.AddWorkItemCommentArgs{
		Request: &workitemtracking.CommentCreate{Text: &text}, Project: &c.Project, WorkItemId: &id, Format: commentFormat(markdown),
	})
	if err != nil {
		return nil, mapErr(err)
	}
	out := toComment(*cm)
	return &out, nil
}

// UpdateComment replaces the text of comment commentID of work item id.
func (c *WorkItemClient) UpdateComment(ctx context.Context, id, commentID int, text string, markdown bool) (*WorkItemComment, error) {
	cm, err := c.Wit.UpdateWorkItemComment(ctx, workitemtracking.UpdateWorkItemCommentArgs{
		Request: &workitemtracking.CommentUpdate{Text: &text}, Project: &c.Project, WorkItemId: &id, CommentId: &commentID,
		Format: commentFormat(markdown),
	})
	if err != nil {
		return nil, mapErr(err)
	}
	out := toComment(*cm)
	return &out, nil
}

// DeleteComment soft-deletes comment commentID of work item id.
func (c *WorkItemClient) DeleteComment(ctx context.Context, id, commentID int) error {
	return mapErr(c.Wit.DeleteComment(ctx, workitemtracking.DeleteCommentArgs{Project: &c.Project, WorkItemId: &id, CommentId: &commentID}))
}

// Updates returns the whole change log of work item id, oldest first.
func (c *WorkItemClient) Updates(ctx context.Context, id int) ([]WorkItemUpdate, error) {
	var out []WorkItemUpdate
	for skip := 0; ; {
		top := updatesPageSize
		page, err := c.Wit.GetUpdates(ctx, workitemtracking.GetUpdatesArgs{Id: &id, Project: &c.Project, Top: &top, Skip: &skip})
		if err != nil {
			return nil, mapErr(err)
		}
		for _, u := range deref(page) {
			out = append(out, toUpdate(u))
		}
		if len(deref(page)) < top {
			return out, nil
		}
		skip += len(*page)
	}
}

// RelationTypes returns the organization's work item link and resource link types.
func (c *WorkItemClient) RelationTypes(ctx context.Context) ([]RelationType, error) {
	types, err := c.Wit.GetRelationTypes(ctx, workitemtracking.GetRelationTypesArgs{})
	if err != nil {
		return nil, mapErr(err)
	}
	var out []RelationType
	for _, t := range deref(types) {
		attrs := deref(t.Attributes)
		usage, _ := attrs["usage"].(string)
		enabled, _ := attrs["enabled"].(bool)
		out = append(out, RelationType{Name: deref(t.Name), ReferenceName: deref(t.ReferenceName), Usage: usage, Enabled: enabled})
	}
	return out, nil
}

// QueryWIQL runs a WIQL query, in the context of team when not "" (for team macros). top is
// sent as $top when > 0. A query over the 20,000-item limit returns an error suggesting
// narrower filters.
func (c *WorkItemClient) QueryWIQL(ctx context.Context, wiql, team string, top int) (*QueryResult, error) {
	res, err := c.Wit.QueryByWiql(ctx, workitemtracking.QueryByWiqlArgs{
		Wiql: &workitemtracking.Wiql{Query: &wiql}, Project: &c.Project, Team: &team, Top: topArg(top),
	})
	if err != nil {
		return nil, queryErr(err)
	}
	return toQueryResult(*res), nil
}

// QueryByID runs the saved query id (a GUID) like QueryWIQL.
func (c *WorkItemClient) QueryByID(ctx context.Context, id, team string, top int) (*QueryResult, error) {
	qid, err := uuid.Parse(id)
	if err != nil {
		return nil, fmt.Errorf("invalid query ID %q", id)
	}
	res, err := c.Wit.QueryById(ctx, workitemtracking.QueryByIdArgs{Id: &qid, Project: &c.Project, Team: &team, Top: topArg(top)})
	if err != nil {
		return nil, queryErr(err)
	}
	return toQueryResult(*res), nil
}

// Query returns the saved query or folder idOrPath (a GUID or a path such as
// "Shared Queries/Bugs"), with its WIQL when expandWiql and children down to depth when > 0.
func (c *WorkItemClient) Query(ctx context.Context, idOrPath string, depth int, expandWiql bool) (*QueryItem, error) {
	args := workitemtracking.GetQueryArgs{Project: &c.Project, Query: &idOrPath}
	if depth > 0 {
		args.Depth = &depth
	}
	if expandWiql {
		args.Expand = &workitemtracking.QueryExpandValues.Wiql
	}
	q, err := c.Wit.GetQuery(ctx, args)
	if err != nil {
		return nil, mapErr(err)
	}
	out := toQueryItem(*q)
	return &out, nil
}

// Queries returns the root query folders (My Queries, Shared Queries) with children down to
// depth when > 0.
func (c *WorkItemClient) Queries(ctx context.Context, depth int) ([]QueryItem, error) {
	args := workitemtracking.GetQueriesArgs{Project: &c.Project}
	if depth > 0 {
		args.Depth = &depth
	}
	qs, err := c.Wit.GetQueries(ctx, args)
	if err != nil {
		return nil, mapErr(err)
	}
	return toQueryItems(deref(qs)), nil
}

// ClassificationNode returns the node at path (relative to the root, "" for the root) of group
// "areas" or "iterations", with children down to depth when > 0. A missing node is a 404
// HTTPError.
func (c *WorkItemClient) ClassificationNode(ctx context.Context, group, path string, depth int) (*ClassificationNode, error) {
	g := workitemtracking.TreeStructureGroup(group)
	args := workitemtracking.GetClassificationNodeArgs{Project: &c.Project, StructureGroup: &g}
	if path != "" {
		args.Path = &path
	}
	if depth > 0 {
		args.Depth = &depth
	}
	n, err := c.Wit.GetClassificationNode(ctx, args)
	if err != nil {
		return nil, mapErr(err)
	}
	out := toClassificationNode(*n)
	return &out, nil
}

// topArg is top as an optional $top: nil (no limit) when <= 0.
func topArg(top int) *int {
	if top <= 0 {
		return nil
	}
	return &top
}

// queryErr is mapErr with VS402337 (over 20,000 results) turned into advice.
func queryErr(err error) error {
	err = mapErr(err)
	var he *HTTPError
	if errors.As(err, &he) && strings.Contains(he.Message, "VS402337") {
		return &HTTPError{StatusCode: he.StatusCode, TypeKey: he.TypeKey,
			Message: "the query matches more than 20,000 work items; narrow the filters or lower the limit with -L"}
	}
	return err
}

func commentFormat(markdown bool) *workitemtracking.CommentFormat {
	if markdown {
		return &workitemtracking.CommentFormatValues.Markdown
	}
	return &workitemtracking.CommentFormatValues.Html
}

func toTime(t *azuredevops.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return t.Time
}

func toRelations(rels []workitemtracking.WorkItemRelation) []WorkItemRelation {
	var out []WorkItemRelation
	for _, r := range rels {
		out = append(out, WorkItemRelation{Rel: deref(r.Rel), URL: deref(r.Url), Attributes: deref(r.Attributes)})
	}
	return out
}

func toWorkItem(w workitemtracking.WorkItem) WorkItem {
	return WorkItem{
		ID: deref(w.Id), Rev: deref(w.Rev), Fields: deref(w.Fields), Relations: toRelations(deref(w.Relations)), URL: deref(w.Url),
	}
}

func toComment(c workitemtracking.Comment) WorkItemComment {
	by := deref(c.CreatedBy)
	return WorkItemComment{
		ID: deref(c.Id), Version: deref(c.Version), Text: deref(c.Text), Format: string(deref(c.Format)),
		CreatedBy:   IdentityRef{ID: deref(by.Id), DisplayName: deref(by.DisplayName), UniqueName: deref(by.UniqueName)},
		CreatedDate: toTime(c.CreatedDate), ModifiedDate: toTime(c.ModifiedDate), IsDeleted: deref(c.IsDeleted), URL: deref(c.Url),
	}
}

func toUpdate(u workitemtracking.WorkItemUpdate) WorkItemUpdate {
	by, rels := deref(u.RevisedBy), deref(u.Relations)
	out := WorkItemUpdate{
		ID: deref(u.Id), Rev: deref(u.Rev), RevisedDate: toTime(u.RevisedDate),
		RevisedBy:      IdentityRef{ID: idString(by.Id), DisplayName: deref(by.DisplayName), UniqueName: deref(by.UniqueName)},
		RelationsAdded: toRelations(deref(rels.Added)), RelationsRemoved: toRelations(deref(rels.Removed)),
	}
	if u.Fields != nil {
		out.Fields = make(map[string]FieldChange, len(*u.Fields))
		for k, f := range *u.Fields {
			out.Fields[k] = FieldChange{OldValue: f.OldValue, NewValue: f.NewValue}
		}
	}
	return out
}

func toQueryItem(q workitemtracking.QueryHierarchyItem) QueryItem {
	return QueryItem{
		ID: idString(q.Id), Name: deref(q.Name), Path: deref(q.Path), IsFolder: deref(q.IsFolder),
		QueryType: string(deref(q.QueryType)), IsPublic: deref(q.IsPublic), HasChildren: deref(q.HasChildren),
		Wiql: deref(q.Wiql), Children: toQueryItems(deref(q.Children)),
	}
}

func toQueryItems(qs []workitemtracking.QueryHierarchyItem) []QueryItem {
	var out []QueryItem
	for _, q := range qs {
		out = append(out, toQueryItem(q))
	}
	return out
}

func toQueryResult(r workitemtracking.WorkItemQueryResult) *QueryResult {
	out := &QueryResult{QueryType: string(deref(r.QueryType))}
	for _, col := range deref(r.Columns) {
		out.Columns = append(out.Columns, QueryColumn{ReferenceName: deref(col.ReferenceName), Name: deref(col.Name)})
	}
	for _, w := range deref(r.WorkItems) {
		out.IDs = append(out.IDs, deref(w.Id))
	}
	for _, l := range deref(r.WorkItemRelations) {
		out.Links = append(out.Links, WorkItemLink{Rel: deref(l.Rel), SourceID: deref(deref(l.Source).Id), TargetID: deref(deref(l.Target).Id)})
	}
	return out
}

func toClassificationNode(n workitemtracking.WorkItemClassificationNode) ClassificationNode {
	out := ClassificationNode{ID: deref(n.Id), Name: deref(n.Name), Path: deref(n.Path), HasChildren: deref(n.HasChildren)}
	for _, ch := range deref(n.Children) {
		out.Children = append(out.Children, toClassificationNode(ch))
	}
	return out
}

// PullRequestArtifactURL is the artifact URL work items link to for pull request id of the
// repository repoID in the project projectID (both GUIDs).
func PullRequestArtifactURL(projectID, repoID string, id int) string {
	return fmt.Sprintf("vstfs:///Git/PullRequestId/%s%%2F%s%%2F%d", projectID, repoID, id)
}

// CommitArtifactURL is the artifact URL work items link to for commit sha (the full 40
// characters) of the repository repoID in the project projectID (both GUIDs).
func CommitArtifactURL(projectID, repoID, sha string) string {
	return fmt.Sprintf("vstfs:///Git/Commit/%s%%2F%s%%2F%s", projectID, repoID, sha)
}

// BranchArtifactURL is the artifact URL work items link to for branch (with or without
// "refs/heads/") of the repository repoID in the project projectID (both GUIDs). The branch is
// URL-encoded after "GB", so feature/x is GBfeature%2Fx.
func BranchArtifactURL(projectID, repoID, branch string) string {
	b := strings.ReplaceAll(url.QueryEscape(strings.TrimPrefix(branch, "refs/heads/")), "+", "%20")
	return fmt.Sprintf("vstfs:///Git/Ref/%s%%2F%s%%2FGB%s", projectID, repoID, b)
}

// AddRelation adds a relation of type rel from work item id to url, with attributes when not
// nil. An existing relation is success.
func (c *WorkItemClient) AddRelation(ctx context.Context, id int, rel, url string, attributes map[string]any) error {
	value := map[string]any{"rel": rel, "url": url}
	if attributes != nil {
		value["attributes"] = attributes
	}
	op, path := webapi.OperationValues.Add, "/relations/-"
	_, err := c.Wit.UpdateWorkItem(ctx, workitemtracking.UpdateWorkItemArgs{Id: &id, Document: &[]webapi.JsonPatchOperation{{
		Op: &op, Path: &path, Value: value,
	}}})
	if err = mapErr(err); err != nil && strings.Contains(err.Error(), "Relation already exists.") {
		return nil
	}
	return err
}

// RemoveRelation removes the first relation of work item id with type rel and URL url (both
// case insensitively); its other relations are kept. It returns false when there is none.
func (c *WorkItemClient) RemoveRelation(ctx context.Context, id int, rel, url string) (bool, error) {
	item, err := c.Wit.GetWorkItem(ctx, workitemtracking.GetWorkItemArgs{Id: &id, Expand: &workitemtracking.WorkItemExpandValues.Relations})
	if err != nil {
		return false, mapErr(err)
	}
	var rels []workitemtracking.WorkItemRelation
	if item.Relations != nil {
		rels = *item.Relations
	}
	i := slices.IndexFunc(rels, func(r workitemtracking.WorkItemRelation) bool {
		return r.Rel != nil && strings.EqualFold(*r.Rel, rel) && r.Url != nil && strings.EqualFold(*r.Url, url)
	})
	if i < 0 {
		return false, nil
	}
	// The rev test fails the patch if the work item changed since it was read.
	test, remove, revPath, relPath := webapi.OperationValues.Test, webapi.OperationValues.Remove, "/rev", fmt.Sprintf("/relations/%d", i)
	_, err = c.Wit.UpdateWorkItem(ctx, workitemtracking.UpdateWorkItemArgs{Id: &id, Document: &[]webapi.JsonPatchOperation{
		{Op: &test, Path: &revPath, Value: item.Rev},
		{Op: &remove, Path: &relPath},
	}})
	if err != nil {
		return false, mapErr(err)
	}
	return true, nil
}
