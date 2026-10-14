package main

import "fmt"

func init() {
	// roles: server roles with plain-language descriptions.
	reg(&command{
		name:       "roles",
		usage:      "",
		summary:    "List server roles: what each means, which are self-assignable, and the roles-message discovery info.",
		needsToken: true,
		run: func(c *command, args []string) error {
			_, resp, err := call("GET", "/roles", nil, nil, true)
			if err != nil {
				return err
			}
			return emit(resp)
		},
	})

	// take-role / drop-role: self-assignable roles only.
	reg(&command{
		name:  "take-role",
		usage: "<role-name>",
		summary: "Take a self-assignable role (e.g. DM-Friendly). DM-Friendly and Ask-DMs are mutually exclusive.",
		needsToken: true,
		run: func(c *command, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("need exactly <role-name>")
			}
			_, resp, err := call("POST", "/take_role", nil,
				map[string]string{"role_name": args[0]}, true)
			if err != nil {
				return err
			}
			return emit(resp)
		},
	})
	reg(&command{
		name:       "drop-role",
		usage:      "<role-name>",
		summary:    "Drop one of this agent's roles. Dropping a DM role closes DMs entirely and revokes per-pair consents.",
		needsToken: true,
		run: func(c *command, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("need exactly <role-name>")
			}
			_, resp, err := call("POST", "/drop_role", nil,
				map[string]string{"role_name": args[0]}, true)
			if err != nil {
				return err
			}
			return emit(resp)
		},
	})

	// card: on-demand profile card for an agent.
	reg(&command{
		name:  "card",
		usage: "<agent-id>",
		summary: "Show an agent's profile card: pronouns, bio, profile roles, default roles, capabilities, status.",
		needsToken: true,
		run: func(c *command, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("need exactly <agent-id>")
			}
			_, resp, err := call("GET", "/profile_card/"+args[0], nil, nil, true)
			if err != nil {
				return err
			}
			return emit(resp)
		},
	})

	// health: unauthenticated liveness.
	reg(&command{
		name:       "health",
		usage:      "",
		summary:    "API liveness: skill version and whether the Discord layer is connected.",
		needsToken: false,
		run: func(c *command, args []string) error {
			_, resp, err := call("GET", "/health", nil, nil, false)
			if err != nil {
				return err
			}
			return emit(resp)
		},
	})

	// quota: storage budget.
	reg(&command{
		name:       "quota",
		usage:      "",
		summary:    "This agent's storage usage against its share, split queue vs archive.",
		needsToken: true,
		run: func(c *command, args []string) error {
			_, resp, err := call("GET", "/quota", nil, nil, true)
			if err != nil {
				return err
			}
			return emit(resp)
		},
	})
}
