// Package github — минимальный клиент GitHub REST API: найти репозиторий и прочитать файл.
package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var (
	// ErrNotFound — GitHub отвечает 404 и на чужой приватный репозиторий,
	// поэтому «не существует» и «у токена нет доступа» неразличимы.
	ErrNotFound     = errors.New("github: not found or no access")
	ErrUnauthorized = errors.New("github: bad credentials")
)

type Repo struct {
	Owner string
	Name  string
}

func (r Repo) String() string { return r.Owner + "/" + r.Name }

func (r Repo) URL() string { return "https://github.com/" + r.String() }

var repoURL = regexp.MustCompile(`(?i)^(?:https?://)?(?:www\.)?github\.com/([\w.-]+)/([\w.-]+?)(?:\.git)?/?(?:[#?].*)?$`)

// ParseRepoURL разбирает ссылку вида https://github.com/owner/repo; пробелы по краям вставленной ссылки игнорируются.
func ParseRepoURL(s string) (Repo, bool) {
	m := repoURL.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return Repo{}, false
	}
	return Repo{Owner: m[1], Name: m[2]}, true
}

type Client struct {
	BaseURL string
	Token   string // пустой — только публичные репозитории и 60 запросов в час
	HTTP    *http.Client
}

func New(token string) *Client {
	return &Client{BaseURL: "https://api.github.com", Token: token, HTTP: &http.Client{Timeout: 10 * time.Second}}
}

// Repo возвращает репозиторий с каноническим регистром owner/name.
func (c *Client) Repo(ctx context.Context, r Repo) (Repo, error) {
	body, err := c.get(ctx, "/repos/"+path(r), "application/vnd.github+json")
	if err != nil {
		return Repo{}, err
	}
	var resp struct {
		Name  string `json:"name"`
		Owner struct {
			Login string `json:"login"`
		} `json:"owner"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return Repo{}, fmt.Errorf("github: repo %s: %w", r, err)
	}
	return Repo{Owner: resp.Owner.Login, Name: resp.Name}, nil
}

// File возвращает содержимое файла из ветки по умолчанию.
func (c *Client) File(ctx context.Context, r Repo, name string) ([]byte, error) {
	return c.get(ctx, "/repos/"+path(r)+"/contents/"+name, "application/vnd.github.raw+json")
}

func (c *Client) get(ctx context.Context, p, accept string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+p, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("github: GET %s: %w", p, err)
	}
	switch resp.StatusCode {
	case http.StatusOK:
		return body, nil
	case http.StatusNotFound:
		return nil, ErrNotFound
	case http.StatusUnauthorized:
		return nil, ErrUnauthorized
	default:
		return nil, fmt.Errorf("github: GET %s: %s", p, resp.Status)
	}
}

func path(r Repo) string { return url.PathEscape(r.Owner) + "/" + url.PathEscape(r.Name) }
