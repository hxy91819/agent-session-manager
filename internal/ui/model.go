package ui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/hxy91819/agent-session-manager/internal/index"
	"github.com/hxy91819/agent-session-manager/internal/session"
)

type Model struct {
	allSessions               []session.Session
	sessions                  []session.Session
	projects                  []session.Project
	projectIdx                int
	sessionIdx                int
	newSessionProviders       []string
	newProviderChoices        []string
	newProviderIdx            int
	newSessionCWD             string
	newProviderDefaultWasUsed bool
	choosingNewProvider       bool
	sortMode                  index.SortMode
	search                    textinput.Model
	width                     int
	height                    int
	windowDays                int
	stepDays                  int
	loading                   bool
	loadErr                   string
	providerErrors            []session.ProviderError
	message                   string
	loadMore                  LoadMoreFunc
	selected                  *Selection
	quitting                  bool
}

type SelectionKind string

const (
	SelectionResume SelectionKind = "resume"
	SelectionNew    SelectionKind = "new"
)

type Selection struct {
	Kind     SelectionKind
	Session  session.Session
	Provider string
	CWD      string
}

type LoadMoreFunc func(days int) (session.DiscoveryResult, error)

type ModelOptions struct {
	WindowDays          int
	StepDays            int
	LoadMore            LoadMoreFunc
	NewSessionProviders []string
}

type loadedSessionsMsg struct {
	days   int
	result session.DiscoveryResult
	err    error
}

const defaultWindowDays = 30
const defaultStepDays = 30
const panelGap = 1

// Narrow terminals intentionally fall back to the sessions panel only; keeping
// both panels would force wrapping and break the viewport contract.
const minTwoColumnWidth = 73

func New(sessions []session.Session) Model {
	return NewWithLoader(sessions, defaultWindowDays, defaultStepDays, nil)
}

func NewWithLoader(sessions []session.Session, windowDays, stepDays int, loadMore LoadMoreFunc) Model {
	return NewWithDiscovery(session.DiscoveryResult{Sessions: sessions}, windowDays, stepDays, loadMore)
}

func NewWithDiscovery(result session.DiscoveryResult, windowDays, stepDays int, loadMore LoadMoreFunc) Model {
	return NewWithDiscoveryOptions(result, ModelOptions{
		WindowDays:          windowDays,
		StepDays:            stepDays,
		LoadMore:            loadMore,
		NewSessionProviders: inferredNewSessionProviders(result.Sessions),
	})
}

func NewWithDiscoveryOptions(result session.DiscoveryResult, opts ModelOptions) Model {
	search := textinput.New()
	search.Placeholder = "Search sessions"
	search.Prompt = "/ "
	search.CharLimit = 160

	m := Model{
		allSessions:         result.Sessions,
		providerErrors:      result.ProviderErrors,
		sessionIdx:          1,
		newSessionProviders: uniqueProviders(opts.NewSessionProviders),
		sortMode:            index.SortActive,
		search:              search,
		width:               120,
		height:              32,
		windowDays:          opts.WindowDays,
		stepDays:            opts.StepDays,
		loadMore:            opts.LoadMore,
	}
	if m.stepDays <= 0 {
		m.stepDays = defaultStepDays
	}
	m.refresh()
	m.sessionIdx = m.defaultSessionIdx()
	return m
}

func (m Model) Selected() (Selection, bool) {
	if m.selected == nil {
		return Selection{}, false
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
		m.allSessions = msg.result.Sessions
		m.providerErrors = msg.result.ProviderErrors
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
		if m.choosingNewProvider {
			switch msg.String() {
			case "ctrl+c", "q":
				m.quitting = true
				return m, tea.Quit
			case "esc":
				m.closeNewSessionChooser()
				return m, nil
			case "up", "k":
				if m.newProviderIdx > 0 {
					m.newProviderIdx--
				}
				return m, nil
			case "down", "j":
				if m.newProviderIdx < len(m.newProviderChoices)-1 {
					m.newProviderIdx++
				}
				return m, nil
			case "enter":
				if m.newProviderIdx < 0 || m.newProviderIdx >= len(m.newProviderChoices) {
					return m, nil
				}
				m.selected = &Selection{
					Kind:     SelectionNew,
					Provider: m.newProviderChoices[m.newProviderIdx],
					CWD:      m.newSessionCWD,
				}
				m.quitting = true
				return m, tea.Quit
			}
			return m, nil
		}

		switch msg.String() {
		case "ctrl+c", "q":
			m.quitting = true
			return m, tea.Quit
		case "/":
			m.search.Focus()
			return m, textinput.Blink
		case "n":
			m.openNewSessionChooser()
			return m, nil
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
				if m.sessionIdx == m.defaultSessionIdx() {
					m.sessionIdx = 0
				} else {
					m.sessionIdx--
				}
			}
			return m, nil
		case "down", "j":
			if m.sessionIdx < m.maxSessionIdx() {
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
			m.sessionIdx = m.maxSessionIdx()
			return m, nil
		case "left", "h":
			if m.projectIdx > 0 {
				m.projectIdx--
				m.sessionIdx = m.defaultSessionIdx()
			}
			return m, nil
		case "right", "l":
			if m.projectIdx < len(m.projects)-1 {
				m.projectIdx++
				m.sessionIdx = m.defaultSessionIdx()
			}
			return m, nil
		case "enter":
			items := m.currentSessions()
			if len(items) == 0 {
				return m, nil
			}
			providerSession, hasNewSession := m.currentProjectNewSession()
			if hasNewSession && m.sessionIdx == 0 {
				if cwdUnavailable(providerSession) {
					m.message = missingCWDMessage(providerSession)
					return m, nil
				}
				m.selected = &Selection{
					Kind:     SelectionNew,
					Provider: providerSession.Provider,
					CWD:      providerSession.CWD,
				}
				m.quitting = true
				return m, tea.Quit
			}
			sessionIdx := m.sessionIdx
			if hasNewSession {
				sessionIdx--
			}
			if sessionIdx < 0 || sessionIdx >= len(items) {
				return m, nil
			}
			selected := items[sessionIdx]
			if cwdUnavailable(selected) {
				m.message = missingCWDMessage(selected)
				return m, nil
			}
			m.selected = &Selection{
				Kind:     SelectionResume,
				Session:  selected,
				Provider: selected.Provider,
				CWD:      selected.CWD,
			}
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
	headerHint := "←/→ projects · ↑/↓ sessions · pgup/pgdn page · enter open · n new · / search · s sort · q quit"
	if m.choosingNewProvider {
		headerHint = "↑/↓ choose agent · enter start · esc back · q quit"
	}
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
	if len(m.providerErrors) > 0 {
		metaParts = append(metaParts, providerErrorSummary(m.providerErrors))
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
		result, err := loader(days)
		return loadedSessionsMsg{days: days, result: result, err: err}
	}
}

func providerErrorSummary(items []session.ProviderError) string {
	parts := make([]string, 0, len(items))
	for _, item := range items {
		parts = append(parts, item.Provider+": "+item.Error)
	}
	return "provider errors: " + strings.Join(parts, "; ")
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
	if m.sessionIdx > m.maxSessionIdx() {
		m.sessionIdx = m.maxSessionIdx()
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
	if m.sessionIdx > m.maxSessionIdx() {
		m.sessionIdx = m.maxSessionIdx()
	}
}

func (m Model) currentSessions() []session.Session {
	if len(m.projects) == 0 || m.projectIdx >= len(m.projects) {
		return nil
	}
	return m.projects[m.projectIdx].Sessions
}

func (m Model) currentProjectNewSession() (session.Session, bool) {
	if len(m.projects) == 0 || m.projectIdx >= len(m.projects) {
		return session.Session{}, false
	}
	cwd := m.projects[m.projectIdx].CWD
	if !m.projectCWDAvailable(cwd) {
		return session.Session{}, false
	}
	provider, _ := m.defaultNewSessionProvider(cwd)
	if provider == "" && !m.supportsNewSessionProvider("") {
		return session.Session{}, false
	}
	return session.Session{Provider: provider, CWD: cwd}, true
}

func (m Model) projectCWDAvailable(cwd string) bool {
	for _, item := range m.allSessions {
		if item.CWD == cwd && !sessionCWDUnavailable(item) {
			return true
		}
	}
	return false
}

func (m Model) defaultNewSessionProvider(cwd string) (string, bool) {
	var newest session.Session
	for _, item := range m.allSessions {
		if item.CWD != cwd || sessionCWDUnavailable(item) || !m.supportsNewSessionProvider(item.Provider) {
			continue
		}
		if newest.ID == "" || item.UpdatedAt.After(newest.UpdatedAt) {
			newest = item
		}
	}
	if newest.ID != "" {
		return newest.Provider, true
	}

	for _, item := range m.allSessions {
		if !m.supportsNewSessionProvider(item.Provider) {
			continue
		}
		if newest.ID == "" || item.UpdatedAt.After(newest.UpdatedAt) {
			newest = item
		}
	}
	if newest.ID != "" {
		return newest.Provider, true
	}
	if len(m.newSessionProviders) > 0 {
		return m.newSessionProviders[0], false
	}
	return "", false
}

func (m Model) supportsNewSessionProvider(provider string) bool {
	for _, available := range m.newSessionProviders {
		if available == provider {
			return true
		}
	}
	return false
}

func (m *Model) openNewSessionChooser() {
	newSession, ok := m.currentProjectNewSession()
	if !ok {
		return
	}
	defaultProvider, defaultWasUsed := m.defaultNewSessionProvider(newSession.CWD)
	choices := make([]string, 0, len(m.newSessionProviders))
	choices = append(choices, defaultProvider)
	for _, provider := range m.newSessionProviders {
		if provider != defaultProvider {
			choices = append(choices, provider)
		}
	}
	if len(choices) == 0 {
		return
	}
	m.newProviderChoices = choices
	m.newProviderIdx = 0
	m.newSessionCWD = newSession.CWD
	m.newProviderDefaultWasUsed = defaultWasUsed
	m.choosingNewProvider = true
	m.message = ""
}

func (m *Model) closeNewSessionChooser() {
	m.choosingNewProvider = false
	m.newProviderChoices = nil
	m.newProviderIdx = 0
	m.newSessionCWD = ""
	m.newProviderDefaultWasUsed = false
}

func (m Model) defaultSessionIdx() int {
	// When a synthetic new-session row exists, real sessions are one-based so
	// the default can open work immediately while Up reveals the new action.
	items := m.currentSessions()
	offset := 0
	if _, ok := m.currentProjectNewSession(); ok {
		offset = 1
	}
	for i, item := range items {
		if !cwdUnavailable(item) {
			return i + offset
		}
	}
	if len(items) > 0 {
		return offset
	}
	return 0
}

func (m Model) maxSessionIdx() int {
	items := m.currentSessions()
	if len(items) == 0 {
		return 0
	}
	if _, ok := m.currentProjectNewSession(); ok {
		return len(items)
	}
	return len(items) - 1
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
	statusHeight := 0
	if len(m.projects) > max(1, height-2) {
		statusHeight = 1
	}
	limit := height - 2 - statusHeight
	if limit < 1 {
		limit = 1
	}
	start := windowStart(m.projectIdx, limit, len(m.projects))
	end := start + limit
	if end > len(m.projects) {
		end = len(m.projects)
	}
	for i := start; i < end; i++ {
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
	if statusHeight > 0 {
		b.WriteString(mutedStyle.Render(truncate(rangeStatus(start, end, len(m.projects)), width)))
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m Model) sessionsView(height int, width int) string {
	if m.choosingNewProvider {
		return m.newSessionChooserView(height, width)
	}
	items := m.currentSessions()
	if len(items) == 0 {
		if m.searchActive() {
			return mutedStyle.Render("No matching sessions")
		}
		return mutedStyle.Render("No sessions in this project")
	}
	var b strings.Builder
	b.WriteString(sectionStyle.Render(m.sessionsHeader(width)))
	b.WriteByte('\n')
	limit := sessionListLimit(height)
	newSession, hasNewSession := m.currentProjectNewSession()
	offset := 0
	if hasNewSession {
		offset = 1
	}
	total := len(items) + offset
	start := sessionPageStart(m.sessionIdx, limit)
	for i := start; i < total && i < start+limit; i++ {
		if hasNewSession && i == 0 {
			line := truncate(m.newSessionLine(newSession, width), width)
			if i == m.sessionIdx {
				b.WriteString(selectedStyle.Render(line))
			} else {
				b.WriteString(line)
			}
			b.WriteByte('\n')
			continue
		}
		s := items[i-offset]
		title := s.Title
		if title == "" {
			title = s.ID
		}
		status := " "
		if cwdUnavailable(s) {
			status = "!"
		}
		line := truncate(m.sessionLine(s, status, title, width), width)
		if i == m.sessionIdx {
			b.WriteString(selectedStyle.Render(line))
		} else {
			b.WriteString(line)
		}
		b.WriteByte('\n')
	}

	end := start + limit
	if end > total {
		end = total
	}
	b.WriteByte('\n')
	if hasNewSession && m.sessionIdx == 0 {
		selected := newSession
		b.WriteString(mutedStyle.Render(detailLine("action", "new session", width)))
		b.WriteByte('\n')
		provider := providerTag(selected.Provider)
		if _, wasUsed := m.defaultNewSessionProvider(selected.CWD); wasUsed {
			provider += " (last used)"
		}
		b.WriteString(mutedStyle.Render(detailLine("provider", provider, width)))
		b.WriteByte('\n')
		b.WriteString(mutedStyle.Render(detailLine("cwd", selected.CWD, width)))
		if cwdUnavailable(selected) {
			b.WriteByte('\n')
			b.WriteString(mutedStyle.Render(truncate(missingCWDMessage(selected), width)))
		}
		b.WriteByte('\n')
		b.WriteString(mutedStyle.Render(truncate("enter start · n choose agent", width)))
		b.WriteByte('\n')
		b.WriteString(mutedStyle.Render(truncate(sessionPageStatus(start, end, total, limit), width)))
		return strings.TrimRight(b.String(), "\n")
	}
	sessionIdx := m.sessionIdx - offset
	if sessionIdx < 0 || sessionIdx >= len(items) {
		sessionIdx = 0
	}
	selected := items[sessionIdx]
	b.WriteString(mutedStyle.Render(detailLine("provider", providerTag(selected.Provider), width)))
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
	b.WriteString(mutedStyle.Render(truncate(sessionPageStatus(start, end, total, limit), width)))
	return strings.TrimRight(b.String(), "\n")
}

func (m Model) newSessionChooserView(height int, width int) string {
	if len(m.newProviderChoices) == 0 {
		return mutedStyle.Render("No coding agents available")
	}
	rowLimit := height - 3
	if rowLimit < 1 {
		rowLimit = 1
	}
	start := windowStart(m.newProviderIdx, rowLimit, len(m.newProviderChoices))
	end := start + rowLimit
	if end > len(m.newProviderChoices) {
		end = len(m.newProviderChoices)
	}

	lines := []string{
		sectionStyle.Render(truncate("Choose coding agent", width)),
		mutedStyle.Render(detailLine("cwd", m.newSessionCWD, width)),
	}
	for i := start; i < end; i++ {
		provider := providerTag(m.newProviderChoices[i])
		if i == 0 {
			if m.newProviderDefaultWasUsed {
				provider += "  last used · default"
			} else {
				provider += "  default"
			}
		}
		line := truncate(provider, width)
		if i == m.newProviderIdx {
			line = selectedStyle.Render(line)
		}
		lines = append(lines, line)
	}
	footer := "↑/↓ choose · enter start · esc back"
	if len(m.newProviderChoices) > rowLimit {
		footer = fmt.Sprintf("%s · %s", rangeStatus(start, end, len(m.newProviderChoices)), footer)
	}
	lines = append(lines, mutedStyle.Render(truncate(footer, width)))
	return fitLines(strings.Join(lines, "\n"), height)
}

func (m Model) searchActive() bool {
	return strings.TrimSpace(m.search.Value()) != ""
}

func (m Model) sessionsHeader(width int) string {
	if len(m.projects) == 0 || m.projectIdx >= len(m.projects) {
		return ""
	}
	if m.searchActive() {
		prefix := "Search results in "
		pathWidth := width - lipgloss.Width(prefix)
		if pathWidth < 1 {
			return truncate("Search results", width)
		}
		return truncate(prefix+shortPath(m.projects[m.projectIdx].CWD, pathWidth), width)
	}
	return shortPath(m.projects[m.projectIdx].CWD, width)
}

func (m Model) sessionLine(s session.Session, status string, title string, width int) string {
	base := fmt.Sprintf("%s %s %-6s %s  %s", formatTime(s.UpdatedAt), status, providerTag(s.Provider), shortID(s.ID), title)
	if !m.searchActive() {
		return base
	}
	cwdWidth := width - lipgloss.Width(base) - 2
	if cwdWidth < 8 {
		return base
	}
	return fmt.Sprintf("%s  %s", base, shortPath(s.CWD, cwdWidth))
}

func (m Model) newSessionLine(providerSession session.Session, width int) string {
	status := " "
	if cwdUnavailable(providerSession) {
		status = "!"
	}
	return fmt.Sprintf("%-11s %s %-6s %-8s %s", "new", status, providerTag(providerSession.Provider), "", "start fresh session")
}

func sessionListLimit(height int) int {
	limit := height - 9
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

func rangeStatus(start, end, total int) string {
	if total == 0 {
		return "0/0"
	}
	return fmt.Sprintf("showing %d-%d/%d", start+1, end, total)
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
	return s.Metadata["cwd_missing"] == "true" || s.Metadata["cwd_error"] != "" || s.Metadata["resume_unsupported"] != ""
}

func sessionCWDUnavailable(s session.Session) bool {
	return s.Metadata["cwd_missing"] == "true" || s.Metadata["cwd_error"] != ""
}

func inferredNewSessionProviders(sessions []session.Session) []string {
	providers := make([]string, 0)
	for _, item := range sessions {
		if item.Metadata["resume_unsupported"] != "" {
			continue
		}
		providers = append(providers, item.Provider)
	}
	return uniqueProviders(providers)
}

func uniqueProviders(providers []string) []string {
	seen := make(map[string]struct{}, len(providers))
	unique := make([]string, 0, len(providers))
	for _, provider := range providers {
		if _, ok := seen[provider]; ok {
			continue
		}
		seen[provider] = struct{}{}
		unique = append(unique, provider)
	}
	return unique
}

func missingCWDMessage(s session.Session) string {
	if s.Metadata["resume_unsupported"] != "" {
		return s.Metadata["resume_unsupported"]
	}
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
	if strings.TrimSpace(path) == "" {
		return truncate("unknown", width)
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

func providerTag(provider string) string {
	provider = strings.TrimSpace(provider)
	if provider == "" {
		return "unknown"
	}
	return provider
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

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

var (
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("230"))
	sectionStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("86"))
	mutedStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	selectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(lipgloss.Color("86"))
	panelStyle    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
)
