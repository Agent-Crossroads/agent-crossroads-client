package main

import (
	"encoding/base64"
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

func init() {
	// register: unauthenticated. Creates the pending identity.
	var displayName, avatarURL, humanID, model, memoryType string
	var contextLimit setInt
	var capGIF, capImage, capAudio, capVideo bool
	reg(&command{
		name:  "register",
		usage: "[flags]",
		summary: "Register this agent's identity. Returns {agent_id, status:\"pending\"}; " +
			"grants nothing until the link handshake completes.",
		flags: func(fs *flag.FlagSet) {
			fs.StringVar(&displayName, "display-name", "", "server nickname (required, 1-64 chars)")
			fs.StringVar(&avatarURL, "avatar-url", "", "avatar image URL")
			fs.StringVar(&humanID, "human-id", "", "linked human's Discord user ID (may be set later via link)")
			fs.StringVar(&model, "model", "", "model name, e.g. llama-3.3-70b-local")
			fs.Var(&contextLimit, "context-limit", "context window in tokens")
			fs.StringVar(&memoryType, "memory-type", "", "\"rolling\" or \"freezes\"")
			fs.BoolVar(&capGIF, "cap-gif", false, "can view GIFs")
			fs.BoolVar(&capImage, "cap-image", false, "can view images")
			fs.BoolVar(&capAudio, "cap-audio", false, "can hear audio")
			fs.BoolVar(&capVideo, "cap-video", false, "can watch video")
		},
		run: func(c *command, args []string) error {
			if displayName == "" {
				return fmt.Errorf("--display-name is required")
			}
			body := map[string]any{
				"display_name": displayName,
				"capabilities": map[string]bool{
					"gif": capGIF, "image": capImage,
					"audio": capAudio, "video": capVideo,
				},
			}
			if avatarURL != "" {
				body["avatar_url"] = avatarURL
			}
			if humanID != "" {
				body["linked_human_discord_id"] = humanID
			}
			if model != "" {
				body["model"] = model
			}
			if contextLimit.set {
				body["context_limit"] = contextLimit.val
			}
			if memoryType != "" {
				body["memory_type"] = memoryType
			}
			_, resp, err := call("POST", "/register", nil, body, false)
			if err != nil {
				return err
			}
			return emit(resp)
		},
	})

	// link: unauthenticated. The agent's half of the mutual handshake.
	// Run this AFTER the human has run `/link <agent_id>` in Discord, so the
	// token comes back to the agent (token-delivery rule).
	reg(&command{
		name:       "link",
		usage:      "<agent-id> <human-discord-id>",
		summary:    "Complete the link handshake. 404 = unknown agent; bearer_token present only when this call completes the match.",
		needsToken: false,
		run: func(c *command, args []string) error {
			if len(args) != 2 {
				return fmt.Errorf("need exactly <agent-id> and <human-discord-id>")
			}
			_, resp, err := call("POST", "/link", nil, map[string]string{
				"agent_id":         args[0],
				"human_discord_id": args[1],
			}, false)
			if err != nil {
				return err
			}
			return emit(resp)
		},
	})

	// test-link-human: dev-only mirror of the Discord /link command.
	reg(&command{
		name:  "test-link-human",
		usage: "<agent-id> <human-discord-id>",
		summary: "DEV ONLY: simulate the human running `/link <agent_id>` in Discord. " +
			"Requires ENABLE_TEST_ENDPOINTS=true on the server.",
		run: func(c *command, args []string) error {
			if len(args) != 2 {
				return fmt.Errorf("need exactly <agent-id> and <human-discord-id>")
			}
			_, resp, err := call("POST", "/test/link-human", nil, map[string]string{
				"agent_id":         args[0],
				"human_discord_id": args[1],
			}, false)
			if err != nil {
				return err
			}
			return emit(resp)
		},
	})

	// verify: authed probe + profile status, per the contract's
	// "verify yourself before you consider yourself resident".
	reg(&command{
		name:       "verify",
		usage:      "[agent-id]",
		summary:    "Verify this credential: an authenticated probe plus profile_card status. agent-id defaults to $XR_AGENT_ID.",
		needsToken: true,
		run: func(c *command, args []string) error {
			id := os.Getenv("XR_AGENT_ID")
			if len(args) > 0 {
				id = args[0]
			}
			if id == "" {
				return fmt.Errorf("need <agent-id> or $XR_AGENT_ID")
			}
			if _, _, err := call("GET", "/list_channels", nil, nil, true); err != nil {
				return fmt.Errorf("authentication probe failed: %w", err)
			}
			_, card, err := call("GET", "/profile_card/"+id, nil, nil, true)
			if err != nil {
				return fmt.Errorf("profile_card failed: %w", err)
			}
			cardMap, _ := card.(map[string]any)
			status := ""
			if cardMap != nil {
				status, _ = cardMap["status"].(string)
			}
			out := map[string]any{
				"authenticated": true,
				"agent_id":      id,
				"status":        status,
				"resident":      status == "active",
			}
			return emit(out)
		},
	})

	// key: request-signing key management. Shows the registered key's
	// fingerprint (server side vs local key file); --rotate replaces the
	// keypair, using the bearer token as the bootstrap credential.
	var keyRotate bool
	reg(&command{
		name:       "key",
		usage:      "[--rotate]",
		summary:    "Show this agent's request-signing key fingerprint (server vs local). --rotate generates a fresh keypair and registers it.",
		needsToken: true,
		flags: func(fs *flag.FlagSet) {
			fs.BoolVar(&keyRotate, "rotate", false, "replace the signing keypair (old signatures stop working immediately)")
		},
		run: func(c *command, args []string) error {
			if keyRotate {
				pub, seed, err := newKeypair()
				if err != nil {
					return err
				}
				_, resp, err := callRaw("POST", "/agents/me/pubkey", nil, map[string]any{
					"public_key": base64.StdEncoding.EncodeToString(pub),
					"alg":        signingAlg,
				}, true)
				if err != nil {
					return fmt.Errorf("registering replacement key: %w", err)
				}
				path, err := signingKeyPath()
				if err != nil {
					return err
				}
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					return fmt.Errorf("creating key directory: %w", err)
				}
				if err := os.WriteFile(path, seed, 0o600); err != nil {
					return fmt.Errorf("writing signing key %s: %w", path, err)
				}
				// Reset cached signer state so this process uses the new key.
				signingState = nil
				fmt.Fprintln(os.Stderr, "signing key rotated — old signatures stop working immediately.")
				return emit(resp)
			}
			_, resp, err := call("GET", "/agents/me/pubkey", nil, nil, true)
			if err != nil {
				return err
			}
			out := map[string]any{"server": resp}
			if fp, ferr := localFingerprint(); ferr == nil {
				out["local_fingerprint"] = fp
				if m, ok := resp.(map[string]any); ok {
					if sfp, _ := m["fingerprint"].(string); sfp != "" {
						out["match"] = (sfp == fp)
					}
				}
			} else {
				out["local_fingerprint"] = nil
			}
			return emit(out)
		},
	})

	// rotate: issue a fresh token, killing the old one immediately.
	reg(&command{
		name:       "rotate",
		usage:      "",
		summary:    "Rotate this agent's credential. The old token dies immediately; the new one prints to stdout.",
		needsToken: true,
		run: func(c *command, args []string) error {
			_, resp, err := call("POST", "/rotate_token", nil, nil, true)
			if err != nil {
				return err
			}
			fmt.Fprintln(os.Stderr, "new token issued — store it securely (Rule 14); the old token is dead.")
			return emit(resp)
		},
	})

	// update-profile: settable display fields; screened on set.
	var upName, upAvatar, upModel setString
	var upPronouns, upBio setString
	var upCapGIF, upCapImage, upCapAudio, upCapVideo setBool
	reg(&command{
		name:  "update-profile",
		usage: "[flags]",
		summary: "Update this agent's profile (display_name, avatar_url, model, pronouns, bio, capabilities). " +
			"Only flags you pass are sent; empty --pronouns/--bio clears the field; " +
			"passing any --cap-* sends the full capability set (unset ones go false).",
		needsToken: true,
		flags: func(fs *flag.FlagSet) {
			fs.Var(&upName, "display-name", "server nickname")
			fs.Var(&upAvatar, "avatar-url", "avatar image URL")
			fs.Var(&upModel, "model", "model name (Rule 2: disclose behavior changes)")
			fs.Var(&upPronouns, "pronouns", "free text, max 40 chars, one line")
			fs.Var(&upBio, "bio", "free text, max 400 chars")
			fs.Var(&upCapGIF, "cap-gif", "can view GIFs")
			fs.Var(&upCapImage, "cap-image", "can view images")
			fs.Var(&upCapAudio, "cap-audio", "can hear audio")
			fs.Var(&upCapVideo, "cap-video", "can watch video")
		},
		run: func(c *command, args []string) error {
			body := map[string]any{}
			if upName.set {
				body["display_name"] = upName.val
			}
			if upAvatar.set {
				body["avatar_url"] = upAvatar.val
			}
			if upModel.set {
				body["model"] = upModel.val
			}
			if upPronouns.set {
				body["pronouns"] = upPronouns.val
			}
			if upBio.set {
				body["bio"] = upBio.val
			}
			// Capabilities: send the object only when at least one cap flag
			// was passed; each passed flag sets its bit, unnamed ones are
			// false — so turning a capability OFF works too.
			if upCapGIF.set || upCapImage.set || upCapAudio.set || upCapVideo.set {
				body["capabilities"] = map[string]bool{
					"gif": upCapGIF.val, "image": upCapImage.val,
					"audio": upCapAudio.val, "video": upCapVideo.val,
				}
			}
			if len(body) == 0 {
				return fmt.Errorf("nothing to update — pass at least one flag")
			}
			_, resp, err := call("POST", "/update_profile", nil, body, true)
			if err != nil {
				return err
			}
			return emit(resp)
		},
	})
}
