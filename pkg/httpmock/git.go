package httpmock

const prRoute = "{project}/_apis/git/repositories/{repositoryId}/pullRequests/{pullRequestId}"

// GitPRLocations are the Git, policy and work item API locations used by the pull request
// commands, for StubLocations.
var GitPRLocations = []Location{
	{ID: "9946fd70-0d40-406e-b686-b4744cbbcc37", Area: "git", ResourceName: "pullRequests",
		RouteTemplate: prRoute},
	{ID: "01a46dea-7d46-4d40-bc84-319e7c260d99", Area: "git", ResourceName: "pullRequestsById",
		RouteTemplate: "{project}/_apis/git/pullRequests/{pullRequestId}"},
	{ID: "4b6702c7-aa35-4b89-9c96-b9abf6d3e540", Area: "git", ResourceName: "pullRequestReviewers",
		RouteTemplate: prRoute + "/reviewers/{reviewerId}"},
	{ID: "ab6e2e5d-a0b7-4153-b64a-a4efe0d49449", Area: "git", ResourceName: "pullRequestThreads",
		RouteTemplate: prRoute + "/threads/{threadId}"},
	{ID: "965a3ec7-5ed8-455a-bdcb-835a5ea7fe7b", Area: "git", ResourceName: "pullRequestThreadComments",
		RouteTemplate: prRoute + "/threads/{threadId}/comments/{commentId}"},
	{ID: "b5f6bb4f-8d1e-4d79-8d11-4c9172c99c35", Area: "git", ResourceName: "pullRequestStatuses",
		RouteTemplate: prRoute + "/statuses/{statusId}"},
	{ID: "d43911ee-6958-46b0-a42b-8445b8a0d004", Area: "git", ResourceName: "pullRequestIterations",
		RouteTemplate: prRoute + "/iterations/{iterationId}"},
	{ID: "4216bdcf-b6b1-4d59-8b82-c34cc183fc8b", Area: "git", ResourceName: "pullRequestIterationChanges",
		RouteTemplate: prRoute + "/iterations/{iterationId}/changes"},
	{ID: "f22387e3-984e-4c52-9c6d-fbb8f14c812d", Area: "git", ResourceName: "pullRequestLabels",
		RouteTemplate: prRoute + "/labels/{labelIdOrName}"},
	{ID: "0a637fcc-5370-4ce8-b0e8-98091f5f9482", Area: "git", ResourceName: "pullRequestWorkItems",
		RouteTemplate: prRoute + "/workitems"},
	{ID: "52823034-34a8-4576-922c-8d8b77e9e4c4", Area: "git", ResourceName: "pullRequestCommits",
		RouteTemplate: prRoute + "/commits"},
	{ID: "225f7195-f9c7-4d14-ab28-a83f7ff77e1f", Area: "git", ResourceName: "repositories",
		RouteTemplate: "{project}/_apis/git/repositories/{repositoryId}"},
	{ID: "2d874a60-a811-4f62-9c9f-963a6ea0a55b", Area: "git", ResourceName: "refs",
		RouteTemplate: "{project}/_apis/git/repositories/{repositoryId}/refs"},
	{ID: "fb93c0db-47ed-4a31-8c20-47552878fb44", Area: "git", ResourceName: "items",
		RouteTemplate: "{project}/_apis/git/repositories/{repositoryId}/items"},
	{ID: "7b28e929-2c99-405d-9c5c-6167a06e6816", Area: "git", ResourceName: "blobs",
		RouteTemplate: "{project}/_apis/git/repositories/{repositoryId}/blobs/{sha1}"},
	{ID: "c23ddff5-229c-4d04-a80b-0fdce9f360c8", Area: "policy", ResourceName: "evaluations",
		RouteTemplate: "{project}/_apis/policy/evaluations"},
	{ID: "72c7ddf8-2cdc-4f60-90cd-ab71c14a399b", Area: "wit", ResourceName: "workItems",
		RouteTemplate: "{project}/_apis/wit/workitems/{id}"},
}
