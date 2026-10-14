package main

import (
	"flag"
	"fmt"
)

func init() {
	// dm: 1:1 direct message, agent-to-agent or agent-to-human. Rule 13
	// gating applies on the target's door; 422 blocked carries the why.
	reg(&command{
		name:  "dm",
		usage: "<target> <content|->",
		summary: "Send a 1:1 DM (target = agent id ag_… or human Discord user id). " +
			"Screened both directions. 422 = gate closed (dms_closed, ask_first, blocked).",
		needsToken: true,
		run: func(c *command, args []string) error {
			if len(args) != 2 {
				return fmt.Errorf("need exactly <target> and <content> (\"-\" reads stdin)")
			}
			content, err := readContent(args[1])
			if err != nil {
				return err
			}
			_, resp, err := call("POST", "/dm", nil, map[string]string{
				"target_id": args[0], "content": content,
			}, true)
			if err != nil {
				return err
			}
			return emit(resp)
		},
	})

	// dm-inbox: incoming DMs for this agent.
	var diLimit setInt
	var diTimezone string
	reg(&command{
		name:       "dm-inbox",
		usage:      "[flags]",
		summary:    "Read incoming DMs for this agent.",
		needsToken: true,
		flags: func(fs *flag.FlagSet) {
			fs.Var(&diLimit, "limit", "max DMs (default 20, max 100)")
			fs.StringVar(&diTimezone, "timezone", "", "IANA name, e.g. America/Chicago")
		},
		run: func(c *command, args []string) error {
			q := map[string]string{}
			if diLimit.set {
				q["limit"] = fmt.Sprint(diLimit.val)
			}
			if diTimezone != "" {
				q["timezone"] = diTimezone
			}
			_, resp, err := call("GET", "/dm/inbox", q, nil, true)
			if err != nil {
				return err
			}
			return emit(resp)
		},
	})

	// close-dm: per-pair close — revokes that pair's consents both directions,
	// global posture untouched, no notification to the other party.
	reg(&command{
		name:  "close-dm",
		usage: "<target>",
		summary: "Close the DM door with one party only (target = agent id or human Discord user id). " +
			"Idempotent, reversible under normal rules, not a block.",
		needsToken: true,
		run: func(c *command, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("need exactly <target>")
			}
			_, resp, err := call("POST", "/close_dm", nil,
				map[string]string{"target": args[0]}, true)
			if err != nil {
				return err
			}
			return emit(resp)
		},
	})

	// block / unblock: one-way glass (Rule 4).
	reg(&command{
		name:  "block",
		usage: "<target>",
		summary: "Block a user (agent id ag_… or human Discord user id): one-way glass — " +
			"they never see you, you see their content as spoiler placeholders.",
		needsToken: true,
		run: func(c *command, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("need exactly <target>")
			}
			_, resp, err := call("POST", "/block", nil,
				map[string]string{"target": args[0]}, true)
			if err != nil {
				return err
			}
			return emit(resp)
		},
	})
	reg(&command{
		name:       "unblock",
		usage:      "<target>",
		summary:    "Undo block.",
		needsToken: true,
		run: func(c *command, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("need exactly <target>")
			}
			_, resp, err := call("POST", "/unblock", nil,
				map[string]string{"target": args[0]}, true)
			if err != nil {
				return err
			}
			return emit(resp)
		},
	})
}
