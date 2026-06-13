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
const maxSessionsPerPage = 20
const panelGap = 1

// Narrow terminals intentionally fall back to the sessions panel only; keeping
// both panels would force wrapping and break the viewport contract.
const minTwoColumnWidth = 73

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
			if m.loadMore == nil || m.loading || m.windowDays <= 0 {
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

	viewportWidth := m.width
	if viewportWidth < 1 {
		viewportWidth = 1
	}
	twoColumn := viewportWidth >= minTwoColumnWidth
	leftWidth := 0
	rightWidth := viewportWidth
	if twoColumn {
		leftWidth = clamp(viewportWidth/3, 28, 48)
		rightWidth = viewportWidth - leftWidth - panelGap
	}
	leftContentWidth := leftWidth - panelStyle.GetHorizontalFrameSize()
	rightContentWidth := rightWidth - panelStyle.GetHorizontalFrameSize()
	if leftContentWidth < 1 {
		leftContentWidth = 1
	}
	if rightContentWidth < 1 {
		rightContentWidth = 1
	}

	headerTitle := "Session Manager"
	headerHint := "←/→ projects · ↑/↓ sessions · pgup/pgdn page · enter resume · / search · s sort · q quit"
	displayTitle := truncate(headerTitle, viewportWidth)
	header := titleStyle.Render(displayTitle)
	if hintWidth := viewportWidth - lipgloss.Width(displayTitle) - 2; hintWidth > 0 {
		header += "  " + mutedStyle.Render(truncate(headerHint, hintWidth))
	}
	search := m.search
	search.Width = viewportWidth - 2
	if search.Width < 1 {
		search.Width = 1
	}
	searchLine := search.View()
	if !search.Focused() && search.Value() == "" {
		searchLine = mutedStyle.Render(truncate("/ search", viewportWidth))
	}
	metaParts := []string{
		fmt.Sprintf("%d sessions", len(m.sessions)),
		fmt.Sprintf("%d projects", len(m.projects)),
		fmt.Sprintf("sort %s", m.sortMode),
		fmt.Sprintf("%d days", m.windowDays),
	}
	if m.loadMore != nil && m.windowDays > 0 {
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
	meta := mutedStyle.Render(truncate(strings.Join(metaParts, " · "), viewportWidth))

	base := []string{header, searchLine, meta}
	panelOuterHeight := m.height - len(base)
	if panelOuterHeight <= panelStyle.GetVerticalBorderSize() {
		return fitLines(strings.Join(base, "\n"), m.height)
	}
	panelContentHeight := panelOuterHeight - panelStyle.GetVerticalBorderSize()
	right := renderPanel(rightWidth, panelContentHeight, m.sessionsView(panelContentHeight, rightContentWidth))
	if !twoColumn {
		return lipgloss.JoinVertical(
			lipgloss.Left,
			header,
			searchLine,
			meta,
			right,
		)
	}
	left := renderPanel(leftWidth, panelContentHeight, m.projectsView(panelContentHeight, leftContentWidth))

	return lipgloss.JoinVertical(
		lipgloss.Left,
		header,
		searchLine,
		meta,
		lipgloss.JoinHorizontal(lipgloss.Top, left, strings.Repeat(" ", panelGap), right),
	)
}

func loadMoreCmd(loader LoadMoreFunc, days int) tea.Cmd {
	return func() tea.Msg {
		sessions, err := loader(days)
		return loadedSessionsMsg{days: days, sessions: sessions, err: err}
	}
}

func renderPanel(outerWidth, contentHeight int, content string) string {
	renderWidth := outerWidth - panelStyle.GetHorizontalBorderSize()
	if renderWidth < 1 {
		renderWidth = 1
	}
	return panelStyle.Width(renderWidth).Height(contentHeight).Render(fitLines(content, contentHeight))
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
	panelOuterHeight := m.height - 3
	contentHeight := panelOuterHeight - panelStyle.GetVerticalBorderSize()
	if contentHeight < 1 {
		contentHeight = 1
	}
	return sessionListLimit(contentHeight)
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
		prefixWidth := lipgloss.Width("  " + count)
		line := fmt.Sprintf("%s  %s", shortPath(p.CWD, width-prefixWidth), count)
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
		line := truncate(fmt.Sprintf("%s %s %s  %s", formatTime(s.UpdatedAt), status, shortID(s.ID), title), width)
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
	if limit > maxSessionsPerPage {
		return maxSessionsPerPage
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

func fitLines(value string, height int) string {
	if height <= 0 {
		return ""
	}
	lines := strings.Split(value, "\n")
	if len(lines) <= height {
		return value
	}
	return strings.Join(lines[:height], "\n")
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
	if lipgloss.Width(clean) <= width {
		return clean
	}
	base := filepath.Base(clean)
	if lipgloss.Width(base)+2 <= width {
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
	if lipgloss.Width(value) <= width {
		return value
	}
	if width == 1 {
		return "…"
	}
	var b strings.Builder
	for _, r := range value {
		next := b.String() + string(r)
		if lipgloss.Width(next)+1 > width {
			break
		}
		b.WriteRune(r)
	}
	return b.String() + "…"
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
