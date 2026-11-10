# Slack

Exhale in Slack is for a team: reminders arrive as direct messages you can act on, the whole app lives in the **Home** tab and in one message of the chat, and renewals of shared subscriptions can also be posted to a channel such as `#subscriptions`.

Like the Telegram bot, **the Slack app is yours**. Each team creates its own app in its own workspace; nothing goes through a server of the Exhale project. Exhale connects to Slack over [Socket Mode](https://docs.slack.dev/apis/events-api/using-socket-mode): a WebSocket that Exhale opens outward. It needs no public URL and no open port, so it works for an Exhale running on a laptop or behind a home router.

You need to be a workspace admin, or be allowed to install apps.

## 1. Create the app from the manifest

In Exhale, open **Settings → Slack** and press **Create the Slack app**. Slack opens with everything filled in: pick your workspace and confirm.

If the link does not work for you, do it by hand: open <https://api.slack.com/apps>, press **Create New App → From a manifest**, pick your workspace, choose **YAML** and paste this:

```yaml
display_information:
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
```

What the manifest asks for, and why:

| Setting | Why Exhale needs it |
|---|---|
| `socket_mode_enabled` | Events, button presses and `/exhale` arrive over the socket Exhale opens. No request URL exists anywhere. |
| `interactivity` | The buttons under each message. |
| Home tab and Messages tab | The Home tab shows the app. The Messages tab is the direct message where reminders arrive and where you type answers. |
| `/exhale` slash command | Opens the app from anywhere, links an account (`/exhale link CODE`) and accepts invites (`/exhale join CODE`). |
| Event `app_home_opened` | To draw the Home tab when someone opens it. |
| Event `message.im` | To read what you type **to the app** in its direct message: a name, a price, a link code. Exhale never sees other conversations. |

Bot scopes:

| Scope | Why |
|---|---|
| `chat:write` | Send reminders and update them in place when you press a button. |
| `chat:write.public` | Post in a public channel without being invited first. Only used for the team channel; remove it if you prefer to invite the app yourself. |
| `commands` | The `/exhale` command. |
| `im:history` | Receive the messages typed to the app in its direct message (the `message.im` event). |
| `im:write` | Open the direct message with a member to deliver their reminders. |
| `pins:write` | Pin the app message in the direct message, so it is always one tap away. |
| `users:read` | Know the name of a teammate who joins with an invite. |

## 2. Install it and copy the two tokens

1. In the app's page, open **Install App** and press **Install to Workspace**. Copy the **Bot User OAuth Token**. It starts with `xoxb-`.
2. Open **Basic Information → App-Level Tokens**, press **Generate Token and Scopes**, add the scope `connections:write` and generate. Copy the token. It starts with `xapp-`.

The first token lets Exhale act as the bot. The second one only lets it open the socket.

## 3. Paste them in Exhale

Back in **Settings → Slack**, paste both tokens and press **Connect**. Exhale checks them with Slack before saving anything (`auth.test` for the bot token, `apps.connections.open` for the app-level one) and connects right away, without a restart. The page then shows the workspace and the bot's name.

The tokens are stored in Exhale's database (the `settings` table), like the Telegram token, so they are part of your backups. They are never shown again and never written to the logs. Only an owner can set or remove them.

If you run Exhale from a script or a container you can set them in the environment instead. Both are needed, and they win over anything saved from the web:

```sh
EXHALE_SLACK_BOT_TOKEN=xoxb-…
EXHALE_SLACK_APP_TOKEN=xapp-…
```

## 4. Link your Slack account

Exhale has to know which member you are. In **Settings → Slack**, copy the line under **Link your Slack account** and type it in Slack, in any conversation:

```
/exhale link CODE
```

You can also open the Exhale app in Slack and send it the code alone. A code works once and for 10 minutes; the page makes a new one when it expires. From a terminal, `exhale link slack` prints a code for the owner.

Once linked, the app opens in your direct message with Exhale and in its Home tab. People who are not linked get one line explaining how to link and nothing else.

Every member links their own account the same way, each from their own Exhale session.

## 5. Invite teammates

An owner presses **Invite a teammate** in Settings → Slack and sends the teammate the command it shows:

```
/exhale join CODE
```

They type it in Slack and they are in: a member named after their Slack profile, with their account already linked. They do not need to reach Exhale's web page, which matters when Exhale runs on your machine. An invite works once and for 7 days.

## 6. A channel for the team (optional)

In **Settings → Slack → Team channel**, type a channel such as `#subscriptions` and save. Exhale posts a short note there to prove it can, and from then on every reminder of a **shared** subscription is also posted to that channel, once, whoever it was for.

- **Private subscriptions never go to the channel.** They only reach the direct message of whoever can see them.
- Anyone in the channel who linked their Slack account can press the buttons, and acts as themselves: the activity log says who did it.
- Someone who is not linked gets a private note asking them to link first.
- A button that needs a typed answer (a new amount, for instance) continues in that person's direct message.
- For a private channel, invite the app first: `/invite @Exhale`.

Leave the field empty, or press **Stop posting**, to keep reminders in direct messages only.

## Using it

- **Home tab**: the app. It stays current when anything changes, from the web, Telegram, an AI agent or a teammate.
- **Direct message**: reminders, and the same app as a pinned message. Type a word to open a screen: `home`, `subs`, `add`, `upcoming`, `summary`, `checkup`, `methods`, `settings`.
- **`/exhale`** followed by one of those words works in any conversation and opens the screen in your direct message. Slack keeps the slash for its own commands, so inside the direct message the words go without it.
- While Exhale waits for a typed answer, what you type is the answer. `/exhale home` leaves the question.

## Troubleshooting

| What you see | What to do |
|---|---|
| "Slack does not recognize the bot token" | Copy the **Bot User OAuth Token** again from **Install App**. If you changed scopes, reinstall the app first: Slack issues a new token. |
| "Slack does not accept the app-level token" | The token needs the `connections:write` scope, and **Socket Mode** must be on (Settings → Socket Mode in the app's page). |
| Settings shows "Slack is not connected: invalid_auth" | The app was uninstalled or a token was revoked. Press **Use another app** and paste new tokens. |
| `/exhale` answers "dispatch_failed" or times out | Exhale is not running or cannot reach Slack. Socket Mode only works while the server is up. |
| The app does not answer in the direct message | In the app's page, **App Home** must allow messages in the Messages tab, and **Event Subscriptions** must list `message.im`. Reinstall after changing either. |
| The Home tab is empty | **App Home → Home Tab** must be on and **Event Subscriptions** must list `app_home_opened`. Then leave the tab and open it again. |
| "Slack would not let the app post in #channel" | The channel does not exist, or it is private and the app was not invited. |
| No reminders in Slack | Link your account (step 4). Reminders go to linked accounts at the household's reminder time. |
| Buttons in an old message do nothing | The message may have been retired by a newer app message. Type `home`. |

Slack limits how often an app may call it. At the size of a team this is far from the limits; if Slack ever answers "rate limited", the affected update is skipped and the next change redraws the message.
