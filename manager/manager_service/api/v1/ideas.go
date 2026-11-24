package v1

type IdeaInput struct {
	Title  *string `json:"title"`
	Note   string  `json:"note"`
	Source string  `json:"source"`
	Status *string `json:"status"`
}

type Idea struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Note    string `json:"note"`
	Source  string `json:"source"`
	Status  string `json:"status"`
	Created int64  `json:"created"`
}

type DeleteIdea struct {
	OK      bool   `json:"ok"`
	Deleted string `json:"deleted"`
}

type Error struct {
	Detail string `json:"detail"`
}

type ValidationIssue struct {
	Field   []string `json:"loc"`
	Message string   `json:"msg"`
	Type    string   `json:"type"`
}
