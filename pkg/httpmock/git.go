package httpmock

// GitPRLocations are the Git API locations used by the pull request commands, for StubLocations.
var GitPRLocations = []Location{
	{ID: "9946fd70-0d40-406e-b686-b4744cbbcc37", Area: "git", ResourceName: "pullRequests",
		RouteTemplate: "{project}/_apis/git/repositories/{repositoryId}/pullRequests/{pullRequestId}"},
	{ID: "01a46dea-7d46-4d40-bc84-319e7c260d99", Area: "git", ResourceName: "pullRequestsById",
		RouteTemplate: "{project}/_apis/git/pullRequests/{pullRequestId}"},
	{ID: "4b6702c7-aa35-4b89-9c96-b9abf6d3e540", Area: "git", ResourceName: "pullRequestReviewers",
		RouteTemplate: "{project}/_apis/git/repositories/{repositoryId}/pullRequests/{pullRequestId}/reviewers/{reviewerId}"},
	{ID: "ab6e2e5d-a0b7-4153-b64a-a4efe0d49449", Area: "git", ResourceName: "pullRequestThreads",
		RouteTemplate: "{project}/_apis/git/repositories/{repositoryId}/pullRequests/{pullRequestId}/threads/{threadId}"},
	{ID: "225f7195-f9c7-4d14-ab28-a83f7ff77e1f", Area: "git", ResourceName: "repositories",
		RouteTemplate: "{project}/_apis/git/repositories/{repositoryId}"},
	{ID: "c23ddff5-229c-4d04-a80b-0fdce9f360c8", Area: "policy", ResourceName: "evaluations",
		RouteTemplate: "{project}/_apis/policy/evaluations"},
}
