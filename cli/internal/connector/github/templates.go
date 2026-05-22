package github

// GraphQL queries and mutations used by the github connector. Lifted from the
// original `.archetipo/connectors/github.md` to preserve behaviour and tested
// against `gh api graphql` semantics.

// linkProjectMutation links a Projects v2 project to a repository so the
// project shows up in repo-context tools.
const linkProjectMutation = `
mutation($projectId: ID!, $repoId: ID!) {
  linkProjectV2ToRepository(input: {projectId: $projectId, repositoryId: $repoId}) {
    repository { id nameWithOwner }
  }
}`

// addProjectItemMutation adds an issue to a project board and returns the new item id.
const addProjectItemMutation = `
mutation($projectId: ID!, $contentId: ID!) {
  addProjectV2ItemById(input: {projectId: $projectId, contentId: $contentId}) {
    item { id }
  }
}`

// updateSingleSelectFieldMutation updates a single-select field for one item.
const updateSingleSelectFieldMutation = `
mutation($projectId: ID!, $itemId: ID!, $fieldId: ID!, $optionId: String!) {
  updateProjectV2ItemFieldValue(input: {
    projectId: $projectId,
    itemId: $itemId,
    fieldId: $fieldId,
    value: { singleSelectOptionId: $optionId }
  }) { projectV2Item { id } }
}`

// updateNumberFieldMutation updates a number field for one item.
const updateNumberFieldMutation = `
mutation($projectId: ID!, $itemId: ID!, $fieldId: ID!, $value: Float!) {
  updateProjectV2ItemFieldValue(input: {
    projectId: $projectId,
    itemId: $itemId,
    fieldId: $fieldId,
    value: { number: $value }
  }) { projectV2Item { id } }
}`

// projectFieldsQuery is the lean replacement for `gh project field-list`.
// The gh command fetches every item and every field value too, which costs
// ~100 GraphQL credits on even small boards. This asks only for field metadata.
const projectFieldsQuery = `
query($projectId: ID!) {
  node(id: $projectId) {
    ... on ProjectV2 {
      fields(first: 50) {
        nodes {
          __typename
          ... on ProjectV2Field { id name dataType }
          ... on ProjectV2IterationField { id name dataType }
          ... on ProjectV2SingleSelectField {
            id
            name
            dataType
            options { id name }
          }
        }
      }
    }
  }
}`

// projectItemsQuery is the lean replacement for `gh project item-list`.
// It fetches only issue content and the named ARchetipo fields rather than
// every ProjectV2 field value type.
const projectItemsQuery = `
query($projectId: ID!, $after: String) {
  node(id: $projectId) {
    ... on ProjectV2 {
      items(first: 100, after: $after) {
        pageInfo { endCursor hasNextPage }
        nodes {
          id
          content {
            __typename
            ... on Issue {
              number
              title
              url
              labels(first: 20) { nodes { name } }
            }
          }
          status: fieldValueByName(name: "Status") {
            __typename
            ... on ProjectV2ItemFieldSingleSelectValue { name optionId }
          }
          priority: fieldValueByName(name: "Priority") {
            __typename
            ... on ProjectV2ItemFieldSingleSelectValue { name optionId }
          }
          points: fieldValueByName(name: "Story Points") {
            __typename
            ... on ProjectV2ItemFieldNumberValue { number }
          }
          epic: fieldValueByName(name: "Epic") {
            __typename
            ... on ProjectV2ItemFieldSingleSelectValue { name optionId }
            ... on ProjectV2ItemFieldTextValue { text }
          }
        }
      }
    }
  }
}`
