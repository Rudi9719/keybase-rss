package main

// Subscription links Team/Channel/User to Feed
type Subscription struct {
	Channel string `json:"channel"`
	Team    bool   `json:"is_team"`
	User    string `json:"user"`
	Url     string `json:"url"`
}
type Post struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Link        string `json:"link"`
	Id          string `json:"id"`
	Pubdate     string `json:"pubDate"`
}
