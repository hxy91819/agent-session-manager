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
	message     string
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

const defaultWindowDays = 30
const defaultStepDays = 30

func New(sessions []session.Session) Model {
	return NewWithLoader(sessions, defaultWindowDays, defaultStepDays, nil)
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
		m.stepDays = defaultStepDays
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
			m.message = ""
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
			m.message = ""
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
		case "pgup", "pageup", "ctrl+u":
			m.moveSessionPage(-1)
			return m, nil
		case "pgdown", "pagedown", "ctrl+d":
			m.moveSessionPage(1)
			return m, nil
		case "home", "g":
			m.sessionIdx = 0
			return m, nil
		case "end", "G":
			if items := m.currentSessions(); len(items) > 0 {
				m.sessionIdx = len(items) - 1
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
			if cwdUnavailable(selected) {
				m.message = missingCWDMessage(selected)
				return m, nil
			}
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

	header := titleStyle.Render("Session Manager") + "  " + mutedStyle.Render("←/→ projects · ↑/↓ sessions · pgup/pgdn page · enter resume · / search · s sort · q quit")
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
		metaParts = append(metaParts, fmt.Sprintf("m +%dd", m.stepDays))
	}
	if m.loading {
		metaParts = append(metaParts, "loading...")
	}
	if m.loadErr != "" {
		metaParts = append(metaParts, "load error: "+m.loadErr)
	}
	if m.message != "" {
		metaParts = append(metaParts, m.message)
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

func (m *Model) moveSessionPage(direction int) {
	items := m.currentSessions()
	if len(items) == 0 {
		return
	}
	limit := m.sessionPageSize()
	if direction < 0 {
		m.sessionIdx -= limit
	} else {
		m.sessionIdx += limit
	}
	if m.sessionIdx < 0 {
		m.sessionIdx = 0
	}
	if m.sessionIdx >= len(items) {
		m.sessionIdx = len(items) - 1
	}
}

func (m Model) currentSessions() []session.Session {
	if len(m.projects) == 0 || m.projectIdx >= len(m.projects) {
		return nil
	}
	return m.projects[m.projectIdx].Sessions
}

func (m Model) sessionPageSize() int {
	availableHeight := m.height - 7
	if availableHeight < 8 {
		availableHeight = 8
	}
	return sessionListLimit(availableHeight)
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
		count := fmt.Sprintf("%d", p.Count)
		if missingSessionCount(p.Sessions) > 0 {
			count += "!"
		}
		line := fmt.Sprintf("%s  %s", shortPath(p.CWD, width-8), count)
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
	limit := sessionListLimit(height)
	start := sessionPageStart(m.sessionIdx, limit)
	for i := start; i < len(items) && i < start+limit; i++ {
		s := items[i]
		title := s.Title
		if title == "" {
			title = s.ID
		}
		status := " "
		if cwdUnavailable(s) {
			status = "!"
		}
		line := fmt.Sprintf("%s %s %s  %s", formatTime(s.UpdatedAt), status, shortID(s.ID), truncate(title, width-30))
		if i == m.sessionIdx {
			b.WriteString(selectedStyle.Render(line))
		} else {
			b.WriteString(line)
		}
		b.WriteByte('\n')
	}

	end := start + limit
	if end > len(items) {
		end = len(items)
	}
	selected := items[m.sessionIdx]
	b.WriteByte('\n')
	b.WriteString(mutedStyle.Render(detailLine("cwd", selected.CWD, width)))
	if cwdUnavailable(selected) {
		b.WriteByte('\n')
		b.WriteString(mutedStyle.Render(truncate(missingCWDMessage(selected), width)))
	}
	b.WriteByte('\n')
	b.WriteString(mutedStyle.Render(detailLine("id", selected.ID, width)))
	if selected.Path != "" {
		b.WriteByte('\n')
		b.WriteString(mutedStyle.Render(detailLine("file", selected.Path, width)))
	}
	b.WriteByte('\n')
	b.WriteString(mutedStyle.Render(truncate(sessionPageStatus(start, end, len(items), limit), width)))
	return strings.TrimRight(b.String(), "\n")
}

func sessionListLimit(height int) int {
	limit := height - 8
	if limit < 1 {
		return 1
	}
	return limit
}

func sessionPageStart(cursor, limit int) int {
	if limit <= 0 {
		return 0
	}
	return cursor / limit * limit
}

func sessionPageStatus(start, end, total, limit int) string {
	if total == 0 {
		return "0/0"
	}
	page := start/limit + 1
	pages := (total + limit - 1) / limit
	return fmt.Sprintf("showing %d-%d/%d · page %d/%d · pgup/pgdn", start+1, end, total, page, pages)
}

func detailLine(label, value string, width int) string {
	return truncate(label+": "+value, width)
}

func cwdUnavailable(s session.Session) bool {
	return s.Metadata["cwd_missing"] == "true" || s.Metadata["cwd_error"] != ""
}

func missingCWDMessage(s session.Session) string {
	if s.Metadata["cwd_error"] != "" {
		return "cwd check failed: " + s.Metadata["cwd_error"]
	}
	return "cwd missing: " + s.CWD
}

func missingSessionCount(sessions []session.Session) int {
	count := 0
	for _, s := range sessions {
		if cwdUnavailable(s) {
			count++
		}
	}
	return count
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
