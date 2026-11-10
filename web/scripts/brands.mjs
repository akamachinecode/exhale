// Writes src/lib/brand-table.ts: the brand marks Exhale bundles (ADR 0014,
// ADR 0027), taken from simple-icons (CC0) so no request to a third party
// ever reveals what someone subscribes to. Run: node scripts/brands.mjs
//
// Some well-known services (Microsoft, Adobe, Amazon, Disney…) are not in
// simple-icons at their owners' request; they show their initials, or the
// logo someone uploads.
import { writeFileSync } from "node:fs";
import * as si from "simple-icons";

// keyword(s) -> simple-icons slug. Grouped as people subscribe.
const groups = {
  "Video": [
    ["netflix", "netflix"], ["hbo max|hbo", "hbo"], ["max", "max"], ["apple tv|apple tv+", "appletv"],
    ["youtube|youtube premium", "youtube"], ["youtube music", "youtubemusic"], ["youtube tv", "youtubetv"],
    ["crunchyroll", "crunchyroll"], ["dazn", "dazn"], ["paramount|paramount+", "paramountplus"], ["mubi", "mubi"],
    ["plex|plex pass", "plex"], ["twitch", "twitch"], ["kick.com", "kick"], ["vimeo", "vimeo"], ["sky|sky go|now tv", "sky"],
    ["rakuten", "rakuten"],
  ],
  "Music and audio": [
    ["spotify", "spotify"], ["apple music", "applemusic"], ["deezer", "deezer"], ["tidal", "tidal"],
    ["soundcloud|soundcloud go", "soundcloud"], ["pandora", "pandora"], ["audible", "audible"], ["napster", "napster"],
    ["audiomack", "audiomack"], ["bandcamp", "bandcamp"], ["last.fm|lastfm", "lastdotfm"], ["pocket casts", "pocketcasts"],
    ["overcast", "overcast"], ["iheartradio|iheart", "iheartradio"], ["apple podcasts", "applepodcasts"],
  ],
  "Apple and Google": [
    ["apple|apple one", "apple"], ["icloud|icloud+", "icloud"], ["apple arcade", "applearcade"], ["apple news|apple news+", "applenews"],
    ["google one|google workspace|g suite|gsuite|google", "google"], ["google drive", "googledrive"], ["google photos", "googlephotos"],
    ["google cloud|gcp", "googlecloud"], ["gmail", "gmail"], ["google play|play pass", "googleplay"], ["nest aware|google home", "googlehome"],
    ["gemini|google gemini", "googlegemini"],
  ],
  "News, reading and creators": [
    ["new york times|nytimes|nyt", "newyorktimes"], ["the guardian|guardian", "theguardian"], ["medium.com|medium membership", "medium"],
    ["substack", "substack"], ["patreon", "patreon"], ["onlyfans", "onlyfans"], ["ko-fi|kofi", "kofi"],
    ["buy me a coffee|buymeacoffee", "buymeacoffee"], ["feedly", "feedly"], ["inoreader", "inoreader"], ["instapaper", "instapaper"],
    ["ghost", "ghost"],
  ],
  "Learning": [
    ["duolingo|duolingo super", "duolingo"], ["coursera|coursera plus", "coursera"], ["udemy", "udemy"], ["skillshare", "skillshare"],
    ["codecademy", "codecademy"], ["datacamp", "datacamp"], ["pluralsight", "pluralsight"], ["frontend mentor", "frontendmentor"],
    ["egghead", "egghead"], ["leetcode", "leetcode"], ["quizlet", "quizlet"], ["chess.com", "chessdotcom"],
    ["wolfram|wolfram alpha", "wolfram"], ["overleaf", "overleaf"], ["zotero", "zotero"],
  ],
  "Storage, mail and passwords": [
    ["dropbox", "dropbox"], ["box.com|box business", "box"], ["mega.nz|mega.io|mega pro", "mega"], ["synology", "synology"], ["backblaze", "backblaze"],
    ["wasabi", "wasabi"], ["tresorit", "tresorit"], ["nextcloud", "nextcloud"], ["proton|proton unlimited", "proton"],
    ["proton mail|protonmail", "protonmail"], ["proton drive", "protondrive"], ["proton vpn|protonvpn", "protonvpn"],
    ["tuta|tutanota", "tuta"], ["zoho|zoho mail", "zoho"], ["hey.com", "hey"], ["1password", "1password"], ["bitwarden", "bitwarden"],
    ["lastpass", "lastpass"], ["dashlane", "dashlane"], ["keeper", "keeper"], ["enpass", "enpass"],
  ],
  "VPN and security": [
    ["nordvpn|nord vpn|nordsecurity", "nordvpn"], ["expressvpn|express vpn", "expressvpn"], ["surfshark", "surfshark"],
    ["mullvad", "mullvad"], ["private internet access|pia vpn", "privateinternetaccess"], ["tailscale", "tailscale"],
    ["zerotier", "zerotier"], ["adguard", "adguard"], ["nextdns", "nextdns"], ["kagi", "kagi"], ["malwarebytes", "malwarebytes"],
    ["bitdefender", "bitdefender"], ["kaspersky", "kaspersky"], ["norton|norton 360", "norton"], ["avast", "avast"], ["mcafee", "mcafee"],
  ],
  "Work and creative software": [
    ["notion", "notion"], ["obsidian|obsidian sync", "obsidian"], ["evernote", "evernote"], ["todoist", "todoist"], ["ticktick", "ticktick"],
    ["akiflow", "akiflow"], ["linear", "linear"], ["figma", "figma"], ["sketch", "sketch"], ["framer", "framer"], ["webflow", "webflow"],
    ["miro", "miro"], ["loom", "loom"], ["airtable", "airtable"], ["asana", "asana"], ["clickup", "clickup"], ["trello", "trello"],
    ["jira", "jira"], ["confluence", "confluence"], ["atlassian", "atlassian"], ["calendly", "calendly"], ["grammarly", "grammarly"],
    ["languagetool", "languagetool"], ["deepl", "deepl"], ["zoom", "zoom"], ["webex", "webex"], ["teamviewer", "teamviewer"],
    ["anydesk", "anydesk"], ["raycast", "raycast"], ["setapp", "setapp"], ["sublime text|sublime", "sublimetext"], ["postman", "postman"],
    ["docker", "docker"], ["davinci resolve", "davinciresolve"], ["autodesk|autocad|fusion 360", "autodesk"], ["vsco", "vsco"],
    ["envato|envato elements", "envato"], ["freepik", "freepik"], ["unsplash", "unsplash"], ["font awesome", "fontawesome"],
    ["bitly", "bitly"], ["linktree", "linktree"], ["buffer", "buffer"], ["hootsuite", "hootsuite"], ["mailchimp", "mailchimp"],
    ["brevo|sendinblue", "brevo"], ["convertkit|kit.com", "kit"], ["shopify", "shopify"], ["squarespace", "squarespace"], ["wix", "wix"],
    ["wordpress|wordpress.com", "wordpress"],
  ],
  "Developers and hosting": [
    ["github", "github"], ["github copilot|copilot", "githubcopilot"], ["gitlab", "gitlab"], ["jetbrains", "jetbrains"],
    ["cursor", "cursor"], ["windsurf", "windsurf"], ["replit", "replit"], ["v0", "v0"], ["vercel", "vercel"], ["netlify", "netlify"],
    ["cloudflare", "cloudflare"], ["digitalocean", "digitalocean"], ["hetzner", "hetzner"], ["ovh|ovhcloud", "ovh"],
    ["scaleway", "scaleway"], ["vultr", "vultr"], ["contabo", "contabo"], ["netcup", "netcup"], ["hostinger", "hostinger"],
    ["ionos|1&1|1and1", "ionos"], ["godaddy", "godaddy"], ["namecheap", "namecheap"], ["porkbun", "porkbun"], ["gandi", "gandi"],
    ["namesilo", "namesilo"], ["kinsta", "kinsta"], ["wp engine|wpengine", "wpengine"], ["cloudways", "cloudways"], ["render.com", "render"],
    ["railway.app|railway.com", "railway"], ["fly.io", "flydotio"], ["supabase", "supabase"], ["sentry", "sentry"], ["datadog", "datadog"],
    ["new relic", "newrelic"], ["ngrok", "ngrok"], ["bunny.net|bunnycdn", "bunnydotnet"], ["mailgun", "mailgun"], ["resend", "resend"],
  ],
  "AI": [
    ["claude|claude pro|claude max", "claude"], ["anthropic", "anthropic"], ["perplexity", "perplexity"], ["mistral|le chat", "mistralai"],
    ["hugging face|huggingface", "huggingface"], ["elevenlabs", "elevenlabs"], ["poe", "poe"], ["openrouter", "openrouter"],
  ],
  "Gaming": [
    ["playstation|ps plus|playstation plus", "playstation"], ["steam", "steam"], ["epic games", "epicgames"], ["ea play|ea app", "ea"],
    ["ubisoft|ubisoft+", "ubisoft"], ["battle.net", "battledotnet"], ["geforce now|nvidia", "nvidia"], ["humble bundle|humble choice", "humblebundle"],
    ["roblox", "roblox"], ["discord|discord nitro", "discord"], ["gog|gog.com", "gogdotcom"], ["itch.io", "itchdotio"], ["shadow pc", "shadow"],
  ],
  "Fitness and health": [
    ["strava", "strava"], ["peloton", "peloton"], ["fitbit|fitbit premium", "fitbit"], ["garmin|garmin connect", "garmin"],
    ["headspace", "headspace"], ["komoot", "komoot"], ["alltrails", "alltrails"],
  ],
  "Money": [
    ["revolut", "revolut"], ["wise", "wise"], ["n26", "n26"], ["paypal", "paypal"], ["klarna", "klarna"], ["monzo", "monzo"], ["caixabank", "caixabank"],
  ],
  "Phone and internet": [
    ["movistar|movistar+", "movistar"], ["vodafone", "vodafone"], ["orange", "orange"], ["o2", "o2"], ["verizon|verizon fios", "verizon"],
    ["spectrum", "spectrum"], ["telekom|deutsche telekom", "deutschetelekom"],
  ],
  "Delivery, rides and apps": [
    ["uber|uber one", "uber"], ["uber eats", "ubereats"], ["glovo|glovo prime", "glovo"], ["deliveroo|deliveroo plus", "deliveroo"],
    ["doordash|dashpass", "doordash"], ["instacart|instacart+", "instacart"], ["lyft", "lyft"], ["just eat", "justeat"],
    ["x premium|twitter|twitter blue", "x"], ["reddit|reddit premium", "reddit"], ["telegram|telegram premium", "telegram"],
    ["snapchat|snapchat+", "snapchat"], ["meta verified|meta quest", "meta"], ["tinder", "tinder"], ["xing", "xing"],
    ["tesla", "tesla"], ["bmw|connecteddrive", "bmw"],
  ],
};

let out = `// Generated by scripts/brands.mjs from simple-icons (CC0). Do not edit by hand:
// change the list in that script and run \`node scripts/brands.mjs\`.
// Only the colour and the path of each mark are kept, to keep this chunk small.

export type Brand = { title: string; hex: string; path: string };

const marks: Record<string, Brand> = {
`;
const used = new Set();
const keys = [];
for (const [, list] of Object.entries(groups)) for (const [kw, slug] of list) {
  const k = "si" + slug[0].toUpperCase() + slug.slice(1);
  const icon = si[k];
  if (!icon) throw new Error("missing " + slug);
  if (!used.has(slug)) { used.add(slug); out += `\t${JSON.stringify(slug)}: { title: ${JSON.stringify(icon.title)}, hex: "${icon.hex}", path: ${JSON.stringify(icon.path)} },\n`; }
  for (const w of kw.split("|")) keys.push([w, slug]);
}
out += `};\n\n// Keyword in a subscription's name → mark. The longest keyword found wins,\n// so "GitHub Copilot" beats "GitHub" and "YouTube Music" beats "YouTube".\nexport const keywords: [string, string][] = [\n`;
for (const [g, list] of Object.entries(groups)) {
  out += `\t// ${g}\n`;
  for (const [kw, slug] of list) for (const w of kw.split("|")) out += `\t[${JSON.stringify(w)}, ${JSON.stringify(slug)}],\n`;
}
out += `];\n\nexport { marks };\n`;
writeFileSync(new URL("../src/lib/brand-table.ts", import.meta.url), out);
console.error(used.size, "marks,", keys.length, "keywords");
