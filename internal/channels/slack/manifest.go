package slack

import "net/url"

// Manifest describes the Slack app a team creates in its own workspace
// (api.slack.com/apps → Create New App → From a manifest). It asks for
// exactly what the adapter uses; docs/guides/slack.md explains each scope.
//
// Socket Mode means no URL appears anywhere: events, button presses and the
// slash command all arrive over the WebSocket the app opens outward.
const Manifest = `display_information:
  name: Exhale
  description: Your subscriptions, handled. Reminders you can act on.
  background_color: "#111111"
features:
  app_home:
    home_tab_enabled: true
    messages_tab_enabled: true
    messages_tab_read_only_enabled: false
  bot_user:
    display_name: Exhale
    always_online: true
  slash_commands:
    - command: /exhale
      description: Open Exhale
      usage_hint: "[home | subs | add | upcoming | summary | checkup | methods | settings | link CODE]"
      should_escape: false
oauth_config:
  scopes:
    bot:
      - chat:write
      - chat:write.public
      - commands
      - im:history
      - im:write
      - pins:write
      - users:read
settings:
  event_subscriptions:
    bot_events:
      - app_home_opened
      - message.im
  interactivity:
    is_enabled: true
  org_deploy_enabled: false
  socket_mode_enabled: true
  token_rotation_enabled: false
`

// CreateURL opens Slack's "create an app" page with the manifest filled in.
func CreateURL() string {
	return "https://api.slack.com/apps?new_app=1&manifest_yaml=" + url.QueryEscape(Manifest)
}
