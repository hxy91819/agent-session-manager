package ui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"session-manager/internal/index"
	"session-manager/internal/session"
)

type Model struct {
	allSessions []session.Session
	sessions    []session.Session
	projects    []session.Project
	projectIdx  int
	sessionIdx  int
	sortMode    index.SortMode
	search      textinput.Model
	width       int
	height      int
	windowDays  int
	stepDays    int
	loading     bool
	loadErr     string
	loadMore    LoadMoreFunc
	selected    *session.Session
	quitting    bool
}

type LoadMoreFunc func(days int) ([]session.Session, error)

type loadedSessionsMsg struct {
	days     int
	sessions []session.Session
	err      error
}

func New(sessions []session.Session) Model {
	return NewWithLoader(sessions, 45, 45, nil)
}

func NewWithLoader(sessions []session.Session, windowDays, stepDays int, loadMore LoadMoreFunc) Model {
	search := textinput.New()
	search.Placeholder = "Search sessions"
	search.Prompt = "/ "
	search.CharLimit = 160

	m := Model{
		allSessions: sessions,
		sortMode:    index.SortActive,
		search:      search,
		width:       120,
		height:      32,
		windowDays:  windowDays,
		stepDays:    stepDays,
		loadMore:    loadMore,
	}
	if m.stepDays <= 0 {
		m.stepDays = 45
	}
	m.refresh()
	return m
}

func (m Model) Selected() (session.Session, bool) {
	if m.selected == nil {
		return session.Session{}, false
	}
	return *m.selected, true
}

func (m Model) Init() tea.Cmd {
	return textinput.Blink
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case loadedSessionsMsg:
		m.loading = false
		if msg.err != nil {
			m.loadErr = msg.err.Error()
			return m, nil
		}
		m.loadErr = ""
		m.windowDays = msg.days
		m.allSessions = msg.sessions
		m.refresh()
		return m, nil
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case tea.KeyMsg:
		if m.search.Focused() {
			switch msg.String() {
			case "esc":
				m.search.Blur()
				if m.search.Value() == "" {
					return m, nil
				}
				m.search.SetValue("")
				m.refresh()
				return m, nil
			case "enter":
				m.search.Blur()
				return m, nil
			}
			var cmd tea.Cmd
			m.search, cmd = m.search.Update(msg)
			m.refresh()
			return m, cmd
		}

		switch msg.String() {
		case "ctrl+c", "q":
			m.quitting = true
			return m, tea.Quit
		case "/":
			m.search.Focus()
			return m, textinput.Blink
		case "s":
			m.cycleSort()
			m.refresh()
			return m, nil
		case "m":
			if m.loadMore == nil || m.loading {
				return m, nil
			}
			nextDays := m.windowDays + m.stepDays
			m.loading = true
			m.loadErr = ""
			return m, loadMoreCmd(m.loadMore, nextDays)
		case "up", "k":
			if m.sessionIdx > 0 {
				m.sessionIdx--
			}
			return m, nil
		case "down", "j":
			if m.sessionIdx < len(m.currentSessions())-1 {
				m.sessionIdx++
			}
			return m, nil
		case "left", "h":
			if m.projectIdx > 0 {
				m.projectIdx--
				m.sessionIdx = 0
			}
			return m, nil
		case "right", "l":
			if m.projectIdx < len(m.projects)-1 {
				m.projectIdx++
				m.sessionIdx = 0
			}
			return m, nil
		case "enter":
			items := m.currentSessions()
			if len(items) == 0 {
				return m, nil
			}
			selected := items[m.sessionIdx]
			m.selected = &selected
			m.quitting = true
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m Model) View() string {
	if m.quitting {
		return ""
	}

	availableHeight := m.height - 7
	if availableHeight < 8 {
		availableHeight = 8
	}
	leftWidth := clamp(m.width/3, 28, 48)
	rightWidth := m.width - leftWidth - 5
	if rightWidth < 44 {
		rightWidth = 44
	}

	header := titleStyle.Render("Session Manager") + "  " + mutedStyle.Render("←/→ projects · ↑/↓ sessions · enter resume · / search · s sort · q quit")
	searchLine := m.search.View()
	if !m.search.Focused() && m.search.Value() == "" {
		searchLine = mutedStyle.Render("/ search")
	}
	metaParts := []string{
		fmt.Sprintf("%d sessions", len(m.sessions)),
		fmt.Sprintf("%d projects", len(m.projects)),
		fmt.Sprintf("sort %s", m.sortMode),
		fmt.Sprintf("%d days", m.windowDays),
	}
	if m.loadMore != nil {
		metaParts = append(metaParts, "m load more")
	}
	if m.loading {
		metaParts = append(metaParts, "loading...")
	}
	if m.loadErr != "" {
		metaParts = append(metaParts, "load error: "+m.loadErr)
	}
	meta := mutedStyle.Render(strings.Join(metaParts, " · "))

	left := panelStyle.Width(leftWidth).Height(availableHeight).Render(m.projectsView(availableHeight, leftWidth))
	right := panelStyle.Width(rightWidth).Height(availableHeight).Render(m.sessionsView(availableHeight, rightWidth))

	return lipgloss.JoinVertical(
		lipgloss.Left,
		header,
		searchLine,
		meta,
		lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right),
	)
}

func loadMoreCmd(loader LoadMoreFunc, days int) tea.Cmd {
	return func() tea.Msg {
		sessions, err := loader(days)
		return loadedSessionsMsg{days: days, sessions: sessions, err: err}
	}
}

func (m *Model) refresh() {
	m.sessions = index.FilterAndSort(m.allSessions, index.Query{
		Search: m.search.Value(),
		Sort:   m.sortMode,
	})
	m.projects = index.GroupProjects(m.sessions)
	if m.projectIdx >= len(m.projects) {
		m.projectIdx = len(m.projects) - 1
	}
	if m.projectIdx < 0 {
		m.projectIdx = 0
	}
	if m.sessionIdx >= len(m.currentSessions()) {
		m.sessionIdx = len(m.currentSessions()) - 1
	}
	if m.sessionIdx < 0 {
		m.sessionIdx = 0
	}
}

func (m *Model) cycleSort() {
	switch m.sortMode {
	case index.SortActive:
		m.sortMode = index.SortCreated
	case index.SortCreated:
		m.sortMode = index.SortProject
	default:
		m.sortMode = index.SortActive
	}
}

func (m Model) currentSessions() []session.Session {
	if len(m.projects) == 0 || m.projectIdx >= len(m.projects) {
		return nil
	}
	return m.projects[m.projectIdx].Sessions
}

func (m Model) projectsView(height int, width int) string {
	if len(m.projects) == 0 {
		return mutedStyle.Render("No sessions found")
	}
	var b strings.Builder
	b.WriteString(sectionStyle.Render("Projects"))
	b.WriteByte('\n')
	limit := height - 2
	start := windowStart(m.projectIdx, limit, len(m.projects))
	for i := start; i < len(m.projects) && i < start+limit; i++ {
		p := m.projects[i]
		line := fmt.Sprintf("%s  %d", shortPath(p.CWD, width-8), p.Count)
		if i == m.projectIdx {
			b.WriteString(selectedStyle.Render(line))
		} else {
			b.WriteString(line)
		}
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m Model) sessionsView(height int, width int) string {
	items := m.currentSessions()
	if len(items) == 0 {
		return mutedStyle.Render("No sessions in this project")
	}
	var b strings.Builder
	project := m.projects[m.projectIdx]
	b.WriteString(sectionStyle.Render(shortPath(project.CWD, width)))
	b.WriteByte('\n')
	limit := height - 7
	if limit < 1 {
		limit = 1
	}
	start := windowStart(m.sessionIdx, limit, len(items))
	for i := start; i < len(items) && i < start+limit; i++ {
		s := items[i]
		title := s.Title
		if title == "" {
			title = s.ID
		}
		line := fmt.Sprintf("%s  %s  %s", formatTime(s.UpdatedAt), shortID(s.ID), truncate(title, width-28))
		if i == m.sessionIdx {
			b.WriteString(selectedStyle.Render(line))
		} else {
			b.WriteString(line)
		}
		b.WriteByte('\n')
	}

	selected := items[m.sessionIdx]
	b.WriteByte('\n')
	b.WriteString(mutedStyle.Render("cwd: " + selected.CWD))
	b.WriteByte('\n')
	b.WriteString(mutedStyle.Render("id:  " + selected.ID))
	if selected.Path != "" {
		b.WriteByte('\n')
		b.WriteString(mutedStyle.Render("file: " + selected.Path))
	}
	return strings.TrimRight(b.String(), "\n")
}

func windowStart(cursor, limit, total int) int {
	if limit <= 0 || total <= limit {
		return 0
	}
	start := cursor - limit/2
	if start < 0 {
		return 0
	}
	if start+limit > total {
		return total - limit
	}
	return start
}

func shortPath(path string, width int) string {
	if width <= 0 {
		return ""
	}
	clean := filepath.Clean(path)
	if len(clean) <= width {
		return clean
	}
	base := filepath.Base(clean)
	if len(base)+2 <= width {
		return "…" + string(filepath.Separator) + base
	}
	return truncate(base, width)
}

func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	return t.Local().Format("01-02 15:04")
}

func truncate(value string, width int) string {
	if width <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= width {
		return value
	}
	if width == 1 {
		return "…"
	}
	return string(runes[:width-1]) + "…"
}

func clamp(value, min, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

var (
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("230"))
	sectionStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("86"))
	mutedStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	selectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(lipgloss.Color("86"))
	panelStyle    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
)
