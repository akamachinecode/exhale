import {
	type SimpleIcon,
	si1password,
	siAnthropic,
	siApple,
	siAppletv,
	siAudible,
	siBitwarden,
	siCloudflare,
	siCrunchyroll,
	siDazn,
	siDeezer,
	siDigitalocean,
	siDropbox,
	siDuolingo,
	siFigma,
	siGithub,
	siGithubcopilot,
	siGoogle,
	siGoogledrive,
	siHbo,
	siHetzner,
	siIcloud,
	siJetbrains,
	siLinear,
	siMax,
	siNetflix,
	siNordvpn,
	siNotion,
	siObsidian,
	siPatreon,
	siPerplexity,
	siPlaystation,
	siPlex,
	siProton,
	siRevolut,
	siSpotify,
	siSteam,
	siTailscale,
	siTidal,
	siTwitch,
	siVercel,
	siYoutube,
	siZoom,
} from "simple-icons";

// Brand logos ship inside the app (simple-icons, CC0): no request to a
// third party ever reveals what you subscribe to (ADR 0014). Longest
// keyword wins, so "GitHub Copilot" beats "GitHub".
const table: [string, SimpleIcon][] = [
	["1password", si1password],
	["anthropic", siAnthropic],
	["claude", siAnthropic],
	["apple tv", siAppletv],
	["apple", siApple],
	["audible", siAudible],
	["bitwarden", siBitwarden],
	["cloudflare", siCloudflare],
	["crunchyroll", siCrunchyroll],
	["dazn", siDazn],
	["deezer", siDeezer],
	["digitalocean", siDigitalocean],
	["dropbox", siDropbox],
	["duolingo", siDuolingo],
	["figma", siFigma],
	["github copilot", siGithubcopilot],
	["copilot", siGithubcopilot],
	["github", siGithub],
	["google one", siGoogle],
	["google drive", siGoogledrive],
	["hbo", siHbo],
	["max", siMax],
	["hetzner", siHetzner],
	["icloud", siIcloud],
	["jetbrains", siJetbrains],
	["linear", siLinear],
	["netflix", siNetflix],
	["nordvpn", siNordvpn],
	["notion", siNotion],
	["obsidian", siObsidian],
	["patreon", siPatreon],
	["perplexity", siPerplexity],
	["playstation", siPlaystation],
	["plex", siPlex],
	["proton", siProton],
	["revolut", siRevolut],
	["spotify", siSpotify],
	["steam", siSteam],
	["tailscale", siTailscale],
	["tidal", siTidal],
	["twitch", siTwitch],
	["vercel", siVercel],
	["youtube", siYoutube],
	["zoom", siZoom],
];

export function brandFor(name: string): SimpleIcon | undefined {
	const n = name.toLowerCase();
	let best: [string, SimpleIcon] | undefined;
	for (const entry of table) {
		const re = new RegExp(`(^|[^a-z])${entry[0].replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}([^a-z]|$)`);
		if (re.test(n) && (!best || entry[0].length > best[0].length)) best = entry;
	}
	return best?.[1];
}

/** Picks black or white for a glyph on the brand color. */
export function onBrand(hex: string) {
	const [r, g, b] = [0, 2, 4].map((i) => Number.parseInt(hex.slice(i, i + 2), 16) / 255);
	const l = 0.2126 * r + 0.7152 * g + 0.0722 * b;
	return l > 0.6 ? "#111" : "#fff";
}
