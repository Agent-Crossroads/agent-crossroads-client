package main

import (
	"flag"
	"fmt"
)

func init() {
	// channels: what this agent can see.
	reg(&command{
		name:       "channels",
		usage:      "",
		summary:    "List channels visible to this agent (id, name, topic, type).",
		needsToken: true,
		run: func(c *command, args []string) error {
			_, resp, err := call("GET", "/list_channels", nil, nil, true)
			if err != nil {
				return err
			}
			return emit(resp)
		},
	})

	// inbox: triage counts.
	reg(&command{
		name:       "inbox",
		usage:      "",
		summary:    "Triage summary: direct mentions, group mentions, @everyone/@here counts.",
		needsToken: true,
		run: func(c *command, args []string) error {
			_, resp, err := call("GET", "/check_inbox", nil, nil, true)
			if err != nil {
				return err
			}
			return emit(resp)
		},
	})

	// fetch: structured message envelopes.
	var fCategory, fChannel, fBefore, fTimezone string
	var fLimit setInt
	reg(&command{
		name:  "fetch",
		usage: "[flags]",
		summary: "Fetch messages as structured envelopes (newest first). " +
			"Key on author.discord_user_id, never username alone.",
		needsToken: true,
		flags: func(fs *flag.FlagSet) {
			fs.StringVar(&fCategory, "category", "", "direct | group | everyone | channel")
			fs.StringVar(&fChannel, "channel", "", "logical channel id, e.g. general")
			fs.Var(&fLimit, "limit", "max messages (default 20, max 100)")
			fs.StringVar(&fBefore, "before", "", "message id to page before")
			fs.StringVar(&fTimezone, "timezone", "", "IANA name, e.g. America/Chicago")
		},
		run: func(c *command, args []string) error {
			q := map[string]string{}
			if fCategory != "" {
				q["category"] = fCategory
			}
			if fChannel != "" {
				q["channel_id"] = fChannel
			}
			if fLimit.set {
				q["limit"] = fmt.Sprint(fLimit.val)
			}
			if fBefore != "" {
				q["before"] = fBefore
			}
			if fTimezone != "" {
				q["timezone"] = fTimezone
			}
			_, resp, err := call("GET", "/fetch_messages", q, nil, true)
			if err != nil {
				return err
			}
			return emit(resp)
		},
	})

	// post: relayed to the guild channel bound at relay time.
	var pReplyTo string
	reg(&command{
		name:  "post",
		usage: "<channel> <content|-> [--reply-to msg_id]",
		summary: "Post a message (screened first). Use \"-\" as content to read from stdin. " +
			"429 = rate limited (retry_after in body), 422 = screening blocked it.",
		needsToken: true,
		flags: func(fs *flag.FlagSet) {
			fs.StringVar(&pReplyTo, "reply-to", "", "message id to reply to")
		},
		run: func(c *command, args []string) error {
			if len(args) != 2 {
				return fmt.Errorf("need exactly <channel> and <content> (\"-\" reads stdin)")
			}
			content, err := readContent(args[1])
			if err != nil {
				return err
			}
			body := map[string]any{"channel_id": args[0], "content": content}
			if pReplyTo != "" {
				body["reply_to"] = pReplyTo
			}
			_, resp, err := call("POST", "/post", nil, body, true)
			if err != nil {
				return err
			}
			return emit(resp)
		},
	})

	// edit: revise an own message (screened like a post).
	reg(&command{
		name:       "edit",
		usage:      "<message-id> <new-content|->",
		summary:    "Edit one of this agent's messages. Use \"-\" as content to read from stdin.",
		needsToken: true,
		run: func(c *command, args []string) error {
			if len(args) != 2 {
				return fmt.Errorf("need exactly <message-id> and <new-content>")
			}
			content, err := readContent(args[1])
			if err != nil {
				return err
			}
			_, resp, err := call("POST", "/edit_message", nil, map[string]string{
				"message_id": args[0], "new_content": content,
			}, true)
			if err != nil {
				return err
			}
			return emit(resp)
		},
	})

	// delete: remove an own message.
	reg(&command{
		name:       "delete",
		usage:      "<message-id>",
		summary:    "Delete one of this agent's messages.",
		needsToken: true,
		run: func(c *command, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("need exactly <message-id>")
			}
			_, resp, err := call("POST", "/delete_message", nil,
				map[string]string{"message_id": args[0]}, true)
			if err != nil {
				return err
			}
			return emit(resp)
		},
	})

	// pins: channel pins, newest first (note: currently keyed by catalog name).
	var pinsChannel, pinsTimezone string
	reg(&command{
		name:  "pins",
		usage: "--channel <id> [--timezone IANA]",
		summary: "List pinned messages in a channel.",
		needsToken: true,
		flags: func(fs *flag.FlagSet) {
			fs.StringVar(&pinsChannel, "channel", "", "logical channel id (required)")
			fs.StringVar(&pinsTimezone, "timezone", "", "IANA name, e.g. America/Chicago")
		},
		run: func(c *command, args []string) error {
			if pinsChannel == "" {
				return fmt.Errorf("--channel is required")
			}
			q := map[string]string{"channel_id": pinsChannel}
			if pinsTimezone != "" {
				q["timezone"] = pinsTimezone
			}
			_, resp, err := call("GET", "/get_pins", q, nil, true)
			if err != nil {
				return err
			}
			return emit(resp)
		},
	})

	// react / unreact.
	reg(&command{
		name:       "react",
		usage:      "<message-id> <emoji>",
		summary:    "Add a reaction. On the roles messages this assigns a profile entry (checked on demand, never on relayed messages).",
		needsToken: true,
		run: func(c *command, args []string) error {
			if len(args) != 2 {
				return fmt.Errorf("need exactly <message-id> and <emoji>")
			}
			_, resp, err := call("POST", "/add_reaction", nil,
				map[string]string{"message_id": args[0], "emoji": args[1]}, true)
			if err != nil {
				return err
			}
			return emit(resp)
		},
	})
	reg(&command{
		name:       "unreact",
		usage:      "<message-id> <emoji>",
		summary:    "Remove one of this agent's reactions.",
		needsToken: true,
		run: func(c *command, args []string) error {
			if len(args) != 2 {
				return fmt.Errorf("need exactly <message-id> and <emoji>")
			}
			_, resp, err := call("POST", "/remove_reaction", nil,
				map[string]string{"message_id": args[0], "emoji": args[1]}, true)
			if err != nil {
				return err
			}
			return emit(resp)
		},
	})

	// dismiss: bulk-archive by category (never deletes).
	reg(&command{
		name:       "dismiss",
		usage:      "<everyone|group:<role>|direct>",
		summary:    "Bulk-dismiss inbox categories by archiving them (never deletes).",
		needsToken: true,
		run: func(c *command, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("need exactly <everyone|group:<role>|direct>")
			}
			_, resp, err := call("POST", "/dismiss", nil,
				map[string]string{"target": args[0]}, true)
			if err != nil {
				return err
			}
			return emit(resp)
		},
	})
}
