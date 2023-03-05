// SPDX-FileCopyrightText: 2023 Jon Lundy <jon@xuu.cc>
// SPDX-License-Identifier: BSD-3-Clause

package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/oklog/ulid/v2"
	"go.salty.im/ratchet/client"
)

// You generally won't need this unless you're processing stuff with
// complicated ANSI escape sequences. Turn it on if you notice flickering.
//
// Also keep in mind that high performance rendering only works for programs
// that use the full size of the terminal. We're enabling that below with
// tea.EnterAltScreen().
const useHighPerformanceRenderer = false

var (
	titleStyle = func() lipgloss.Style {
		b := lipgloss.RoundedBorder()
		b.Right = "├"
		return lipgloss.NewStyle().BorderStyle(b).Padding(0, 1)
	}()

	// infoStyle = func() lipgloss.Style {
	// 	b := lipgloss.RoundedBorder()
	// 	b.Left = "┤"
	// 	return titleStyle.Copy().BorderStyle(b)
	// }()
)

type (
	errMsg error
)

type model struct {
	c *client.Client

	them string

	content   *strings.Builder
	ready     bool
	viewport  viewport.Model
	textInput textinput.Model
	err       error
}

func InitialModel(c *client.Client, them string) model {

	ti := textinput.New()
	ti.Placeholder = "Message"
	ti.Prompt = "foo? "
	ti.Focus()
	ti.CharLimit = 156
	ti.Width = 20

	m := model{
		c:         c,
		them:      them,
		content:   &strings.Builder{},
		textInput: ti,
	}
	m.setPrompt()

	client.On(c, func(ctx context.Context, args client.OnOfferSent) { m.Update(args) })
	client.On(c, func(ctx context.Context, args client.OnOfferReceived) { m.Update(args) })
	client.On(c, func(ctx context.Context, args client.OnSessionStarted) { m.Update(args) })
	client.On(c, func(ctx context.Context, args client.OnSessionClosed) { m.Update(args) })
	client.On(c, func(ctx context.Context, args client.OnMessageReceived) { m.Update(args) })
	client.On(c, func(ctx context.Context, args client.OnMessageSent) { m.Update(args) })
	client.On(c, func(ctx context.Context, args client.OnSaltySent) { m.Update(args) })
	client.On(c, func(ctx context.Context, args client.OnSaltyTextReceived) { m.Update(args) })
	client.On(c, func(ctx context.Context, args client.OnSaltyEventReceived) { m.Update(args) })
	client.On(c, func(ctx context.Context, args client.OnOtherReceived) { m.Update(args) })

	return m
}

func (m model) Init() tea.Cmd {
	return textinput.Blink
}
func (m *model) setPrompt() {
	prompt := ""
	if m.them == "" {
		prompt = fmt.Sprintf("%s> ", m.c.Me().String())
	} else {
		prompt = fmt.Sprintf("%s -> %s> ", m.c.Me().String(), m.them)
	}
	m.textInput.Prompt = prompt
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var (
		cmd  tea.Cmd
		cmds []tea.Cmd
	)

	switch msg := msg.(type) {
	case client.OnMessageReceived:
		for _, e := range msg.Msg.Events {
			fmt.Fprintf(m.content, "\033[90m%s :: \033[34m%s\033[90m: \033[0m%s\n", time.Now().Format("15:04:05"), e.Command, strings.Join(e.Args, ", "))
		}
		fmt.Fprintf(m.content, "\033[90m%s <\033[31m%s\033[90m> \033[0m%s\n", msg.Msg.Timestamp.DateTime().Format("15:04:05"), msg.Them, msg.Msg.LiteralText())

	case client.OnMessageSent:
		fmt.Fprintf(m.content, "\033[90m%s <\033[31m%s\033[90m> \033[0m%s\n", msg.Msg.Timestamp.DateTime().Format("15:04:05"), m.c.Me().String(), msg.Msg.LiteralText())

	case client.OnSaltyTextReceived:
		fmt.Fprintf(m.content, "\033[90m%s <\033[34m%s\033[90m> \033[0m%s\n", msg.Msg.Timestamp.DateTime().Format("15:04:05"), msg.Msg.User, msg.Msg.LiteralText())

	case client.OnSaltyEventReceived:
		fmt.Fprintf(m.content, "\033[90m%s :: \033[34m%s\033[90m: \033[0m%s\n", time.Now().Format("15:04:05"), msg.Event.Command, strings.Join(msg.Event.Args, ", "))

	case client.OnSaltySent:
		fmt.Fprintf(m.content, "\033[90m%s <\033[34m%s\033[90m> \033[0m%s\n", msg.Msg.Timestamp.DateTime().Format("15:04:05"), m.c.Me().String(), msg.Msg.LiteralText())

	case client.OnOfferSent:
		fmt.Fprintf(m.content, "\033[90m%s ::: offer sent %s :::\033[0m\n", getTime(msg.ID).Format("15:04:05"), msg.Them)

	case client.OnOfferReceived:
		fmt.Fprintf(m.content, "\033[90m%s ::: offer received %s :::\033[0m\n", getTime(msg.ID).Format("15:04:05"), msg.Them)

	case client.OnSessionStarted:
		fmt.Fprintf(m.content, "\033[90m%s ::: session started %s :::\033[0m\n", getTime(msg.ID).Format("15:04:05"), msg.Them)

	case client.OnSessionClosed:
		fmt.Fprintf(m.content, "\033[90m%s ::: session closed %s :::\033[0m\n", getTime(msg.ID).Format("15:04:05"), msg.Them)

	case client.OnOtherReceived:
		fmt.Fprintf(m.content, "\033[90m%s ::: unknown message: %s\033[0m\n", time.Now().Format("15:04:05"), msg.Raw)

	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			return m, tea.Quit

		case tea.KeyEnter:
			input := m.textInput.Value()
			if input == "" {
				break
			}

			m.textInput.SetValue("")
			m.viewport.GotoBottom()
			ctx := m.c.BaseCTX()

			if strings.HasPrefix(input, "/chat") {
				sp := strings.Fields(input)
				// handle show list of open sessions
				if len(sp) <= 1 {
					err := m.c.Use(ctx, func(ctx context.Context, sm client.SessionManager) error {
						fmt.Fprintln(m.content, "usage: /chat|close username")
						for _, p := range sm.Sessions() {
							fmt.Fprintln(m.content, "sess: ", p.Name)
						}
						return nil
					})
					if err != nil {
						fmt.Fprintf(m.content, "ERR: %s\n", err)
					}
				}

				if m.c.Me().String() == sp[1] {
					fmt.Fprintln(m.content, "ERR: cant racthet with self")
				}

				m.them = sp[1]
				m.setPrompt()

				_, err := m.c.Chat(ctx, m.them)
				if err != nil {
					fmt.Fprintf(m.content, "ERR: %s\n", err)
				}
				break
			}
			if strings.HasPrefix(input, "/close") {
				sp := strings.Fields(input)

				target := m.them

				if len(sp) > 1 {
					target = sp[1]
				}

				if target == "" {
					break
				}

				m.them = ""
				m.setPrompt()

				err := m.c.Close(ctx, target)
				if err != nil {
					fmt.Fprintf(m.content, "ERR: %s\n", err)
				}
				break
			}
			if strings.HasPrefix(input, "/salty") {
				target, msg, _ := strings.Cut(strings.TrimPrefix(input, "/salty "), " ")
				err := m.c.SendSalty(ctx, target, msg)
				if err != nil {
					fmt.Fprintln(m.content, "ERR: ", err)
				}
				break
			}

			if m.them == "" {
				fmt.Fprintln(m.content, "usage: /chat username")
				break
			}

			m.c.Send(ctx, m.them, input)
		}

	case tea.WindowSizeMsg:
		headerHeight := lipgloss.Height(m.headerView())
		footerHeight := lipgloss.Height(m.footerView())
		inputHeight := lipgloss.Height(m.textInput.View())
		verticalMarginHeight := headerHeight + footerHeight + inputHeight

		if !m.ready {
			m.textInput.Width = msg.Width

			// Since this program is using the full size of the viewport we
			// need to wait until we've received the window dimensions before
			// we can initialize the viewport. The initial dimensions come in
			// quickly, though asynchronously, which is why we wait for them
			// here.

			m.viewport = viewport.New(msg.Width, msg.Height-verticalMarginHeight)
			m.viewport.YPosition = headerHeight
			m.viewport.Width = msg.Width
			m.viewport.HighPerformanceRendering = useHighPerformanceRenderer
			m.viewport.SetContent(m.content.String())
			m.viewport.MouseWheelEnabled = true
			m.ready = true

			// This is only necessary for high performance rendering, which in
			// most cases you won't need.
			//
			// Render the viewport one line below the header.
			m.viewport.YPosition = headerHeight + 1
		} else {
			m.viewport.Width = msg.Width
			m.viewport.Height = msg.Height - verticalMarginHeight
		}

		if useHighPerformanceRenderer {
			// Render (or re-render) the whole viewport. Necessary both to
			// initialize the viewport and when the window is resized.
			//
			// This is needed for high-performance rendering only.
			cmds = append(cmds, viewport.Sync(m.viewport))
		}

	case errMsg:
		m.err = msg
		return m, nil
	}

	// Handle keyboard and mouse events in the viewport
	m.viewport.SetContent(m.content.String())

	m.viewport, cmd = m.viewport.Update(msg)
	cmds = append(cmds, cmd)

	m.textInput, cmd = m.textInput.Update(msg)
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}

func (m model) View() string {
	if !m.ready {
		return "\n  Initializing..."
	}
	return fmt.Sprintf(
		"%s\n%s\n%s\n%s",
		m.headerView(),
		m.viewport.View(),
		m.footerView(),
		m.textInput.View(),
	)
}

func (m model) headerView() string {
	title := titleStyle.Render("Ratchet Chat")
	line := strings.Repeat("─", max(0, m.viewport.Width-lipgloss.Width(title)))
	return lipgloss.JoinHorizontal(lipgloss.Center, title, line)
}

func (m model) footerView() string {
	// info := infoStyle.Render(fmt.Sprintf("%3.f%%", m.viewport.ScrollPercent()*100))
	line := strings.Repeat("─", max(0, m.viewport.Width))
	return lipgloss.JoinHorizontal(lipgloss.Center, line)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func getTime(u ulid.ULID) time.Time {
	return time.UnixMilli(int64(u.Time()))
}
