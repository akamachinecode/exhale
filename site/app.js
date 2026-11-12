// Fills the download lists, picks the right build for the visitor, wires the
// install tabs and translates the page into Spanish. The lists render at once
// from VERSION (kept in step by release-please), then the GitHub API fills in
// sizes, dates and any newer release.
(function () {
	"use strict";
	const repo = "akamachinecode/exhale";
	const VERSION = "0.4.0"; // x-release-please-version

	// Every build of a release, in the order each card shows them. `name`
	// spells the file for a version; `match` finds it among the API's assets.
	// `label`, `group` and `kind` are keys of the texts below.
	const BUILDS = [
		{ os: "darwin", arch: "any", group: "g.app", label: "a.mac", kind: "k.dmg", ext: ".dmg", name: () => "Exhale.dmg", match: /^Exhale\.dmg$/ },
		{ os: "darwin", arch: "arm64", group: "g.cli", label: "a.silicon", kind: "k.archive", ext: ".tar.gz", name: (v) => `exhale_${v}_darwin_arm64.tar.gz`, match: /_darwin_arm64\.tar\.gz$/ },
		{ os: "darwin", arch: "amd64", group: "g.cli", label: "a.intel", kind: "k.archive", ext: ".tar.gz", name: (v) => `exhale_${v}_darwin_amd64.tar.gz`, match: /_darwin_amd64\.tar\.gz$/ },
		{ os: "windows", arch: "any", group: "g.app", label: "a.x64", kind: "k.installer", ext: ".exe", name: () => "Exhale-Setup.exe", match: /^Exhale-Setup\.exe$/ },
		{ os: "windows", arch: "amd64", group: "g.cli", label: "a.x64", kind: "k.archive", ext: ".zip", name: (v) => `exhale_${v}_windows_amd64.zip`, match: /_windows_amd64\.zip$/ },
		{ os: "windows", arch: "arm64", group: "g.cli", label: "a.arm64", kind: "k.archive", ext: ".zip", name: (v) => `exhale_${v}_windows_arm64.zip`, match: /_windows_arm64\.zip$/ },
		...["deb", "rpm"].flatMap((pkg) =>
			["amd64", "arm64", "armv7"].map((arch) => ({
				os: "linux", arch, distro: pkg, group: "g." + pkg, label: "a." + arch, kind: "k.package", ext: "." + pkg,
				name: () => `exhale_linux_${arch}.${pkg}`, match: new RegExp(`^exhale_linux_${arch}\\.${pkg}$`),
			})),
		),
		...["amd64", "arm64", "armv7"].map((arch) => ({
			os: "linux", arch, group: "g.any", label: "a." + arch, kind: "k.archive", ext: ".tar.gz",
			name: (v) => `exhale_${v}_linux_${arch}.tar.gz`, match: new RegExp(`_linux_${arch}\\.tar\\.gz$`),
		})),
	];
	const OS_NAMES = { darwin: "macOS", linux: "Linux", windows: "Windows" };
	const CARD = { darwin: "mac", linux: "linux", windows: "windows" };
	const DOWNLOAD_ICON = '<svg viewBox="0 0 16 16" aria-hidden="true"><path d="M8 2v8m0 0L4.5 6.5M8 10l3.5-3.5M3 13h10" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/></svg>';

	// ---- texts ----

	// English for what this script writes; the page's own English stays in
	// the page and is put back when switching from Spanish.
	const en = {
		"pill.default": "Free and open source · Runs on your computer",
		"pill.release": "v{v} is out",
		"cta.any": "Download Exhale", "cta.for": "Download for {os}", "cta.phone": "Get it for your computer",
		"note.default": "Free and open source under the AGPL-3.0. Every build has signed checksums.",
		"note.build": "{extra}v{v} · <code>{ext}</code>{size}. Not your computer? See <a href=\"#download-{card}\">every build</a>.",
		"note.linux": "v{v} for {arch}: <code>.deb</code> for Debian, Ubuntu and Raspberry Pi OS, <code>.rpm</code> for Fedora and openSUSE, or an archive for any distribution.",
		"note.phone": "Exhale runs on a computer; reminders reach your phone.",
		"meta.default": "The latest release, straight from GitHub Releases.",
		"meta.release": "Version {v}{date}. <a href=\"{notes}\">What's new</a>",
		"meta.date": ", released {d}",
		mine: "For this computer", download: "Download", copy: "Copy", copied: "Copied",
		"g.app": "App", "g.cli": "Command line", "g.deb": "Debian, Ubuntu, Raspberry Pi OS", "g.rpm": "Fedora, RHEL, openSUSE", "g.any": "Any distribution",
		"a.mac": "Apple silicon and Intel", "a.silicon": "Apple silicon", "a.intel": "Intel", "a.x64": "x64", "a.amd64": "x86-64", "a.arm64": "ARM64", "a.armv7": "ARM 32-bit",
		"k.dmg": "Disk image", "k.installer": "Installer", "k.archive": "Archive", "k.package": "Package",
	};
	// Spanish for everything, page and script.
	const es = {
		"nav.features": "Funciones", "nav.how": "Cómo funciona", "nav.download": "Descargar", "nav.docs": "Documentación",
		"pill.default": "Libre y de código abierto · Funciona en tu ordenador",
		"pill.release": "Ya está aquí la v{v}",
		h1a: "Respira.", h1b: "Tus suscripciones están bajo control.",
		lead: "Exhale te avisa antes de cada renovación y te deja arreglarla desde el propio aviso: marcarla como pagada, mover la fecha, cambiar la tarjeta, cancelarla. En Telegram, Slack o el navegador. Funciona en tu propio ordenador, así que tus datos se quedan contigo.",
		"cta.any": "Descargar Exhale", "cta.for": "Descargar para {os}", "cta.phone": "Descárgalo en tu ordenador", "cta.other": "Todas las descargas",
		"note.default": "Libre y de código abierto bajo la AGPL-3.0. Cada compilación tiene sumas de comprobación firmadas.",
		"note.build": "{extra}v{v} · <code>{ext}</code>{size}. ¿No es tu ordenador? Mira <a href=\"#download-{card}\">todas las compilaciones</a>.",
		"note.linux": "v{v} para {arch}: <code>.deb</code> para Debian, Ubuntu y Raspberry Pi OS, <code>.rpm</code> para Fedora y openSUSE, o un archivo para cualquier distribución.",
		"note.phone": "Exhale funciona en un ordenador; los avisos te llegan al móvil.",
		"alt.overview": "Resumen de Exhale: gasto mensual y anual, cargos que necesitan atención, los próximos 30 días y el gasto por categoría y por tarjeta",
		"alt.phone": "Exhale en un móvil, con el dock de todas las páginas",
		"feat.kicker": "Qué hace", "feat.title": "El aviso es la app.",
		"feat.sub": "El mensaje que dice que Netflix se renueva en tres días te deja arreglarlo ahí mismo. Se actualiza solo, y la web también.",
		"f1.t": "Avisos que se contestan", "f1.p": "Ya pagado, otra fecha, otro importe o tarjeta, saltar, cancelar, deshacer. Un toque en el mensaje.",
		"f2.t": "Las fechas que cuestan dinero", "f2.p": "Pruebas gratis antes de que cobren, el último día para cancelar, renovaciones que te necesitan, subidas de precio anunciadas.",
		"f3.t": "Cualquier moneda, exacta", "f3.p": "Totales en tu moneda con los tipos del BCE, y cuánto apartar cada mes para los pagos anuales.",
		"f4.t": "Compartido, sin mezclar", "f4.p": "Tu pareja o tus compañeros de piso tienen su cuenta y sus avisos. Divide una suscripción y mira quién debe a quién.",
		"f5.t": "Pregúntale lo que quieras", "f5.p": "En lenguaje natural por chat, o con Claude, ChatGPT y modelos locales por MCP. Cada cambio se previsualiza y se puede deshacer.",
		"f6.t": "Tuyo, en tu ordenador", "f6.p": "Un programa, una carpeta de datos, una copia cada día. Sin nube, sin telemetría, sin cuenta con nosotros, sin versión de pago.",
		"how.kicker": "Cómo funciona", "how.title": "Instalado en un minuto. Después, funciona solo.",
		"s1.t": "Descárgalo y ábrelo", "s1.p": "Exhale arranca en segundo plano y se abre en su propia ventana. Desde entonces arranca solo con tu ordenador.",
		"s2.t": "Crea tu cuenta", "s2.p": "Después sigue los pasos de la página de inicio. Crear tu propio bot de Telegram lleva un minuto y está guiado.",
		"s3.t": "Añade tus suscripciones", "s3.p": "A mano, desde Wallos o una hoja de cálculo, o desde el extracto del banco. Los avisos empiezan ese mismo día.",
		"dl.kicker": "Descargar", "dl.title": "Elige tu ordenador.",
		"meta.default": "La última versión, directamente desde GitHub Releases.",
		"meta.release": "Versión {v}{date}. <a href=\"{notes}\">Novedades</a>",
		"meta.date": ", publicada el {d}",
		"mac.req": "macOS 12 o posterior",
		"mac.note": "Ábrelo y arrastra Exhale a Aplicaciones. <strong>La primera vez</strong>, macOS dice que no puede verificar al desarrollador: ve a <em>Ajustes del Sistema → Privacidad y seguridad</em> y pulsa <em>Abrir igualmente</em>.",
		"win.req": "Windows 10 u 11",
		"win.note": "El instalador no necesita permisos de administrador. Si aparece <strong>SmartScreen</strong>, pulsa <em>Más información → Ejecutar de todas formas</em>.",
		"linux.req": "x86-64, ARM64, Raspberry Pi",
		"linux.note": "El <code>.deb</code> y el <code>.rpm</code> hacen de Exhale un servicio que arranca con el ordenador, y lo añaden a tus aplicaciones.",
		verify: "Verifica una descarga:", "verify.sig": "firma de cosign", "verify.how": "Cómo verificarla", "verify.all": "Todas las versiones",
		"term.title": "En un servidor, o desde la terminal", "term.linux": "Script para Linux", copy: "Copiar",
		"term.docker": "Para un servidor en casa, un NAS, una Raspberry Pi o un VPS: en una máquina siempre encendida no se pierde ni un aviso. <code>docker logs exhale</code> muestra el código de configuración de un solo uso. La <a href=\"docs/install/docker/\">guía de Docker</a> explica Compose, HTTPS y las copias.",
		"term.script": "Para una máquina sin el <code>.deb</code> ni el <code>.rpm</code>. El script comprueba el SHA-256 de lo que descarga antes de instalar, y deja Exhale como un servicio de systemd. <a href=\"install.sh\">Léelo antes</a>, o mira la <a href=\"docs/install/linux/#install-script\">guía de Linux</a>.",
		"faq.kicker": "Preguntas", "faq.title": "Antes de instalar",
		q1: "¿Mi ordenador tiene que estar encendido?", a1: "Los avisos salen mientras Exhale está en marcha. En un ordenador que se suspende, Exhale puede mantenerlo despierto (Ajustes → Este ordenador), y lo que venza mientras dormía se envía al despertar. Lo ideal es un servidor en casa o una Raspberry Pi que nunca se apague.",
		q2: "¿Dónde están mis datos?", a2: "En una carpeta de tu ordenador, copiada cada día. No se envía nada a ningún sitio salvo que configures un canal como Telegram.",
		q3: "¿Puedo usarlo en el móvil?", a3: "Sí. Los avisos te llegan al móvil por Telegram o Slack, y la web funciona en el móvil. Exhale en sí funciona en un ordenador.",
		q4: "¿De verdad es gratis?", a4: "Sí. Exhale es software libre bajo la AGPL-3.0, sin versión de pago, sin cuenta con nosotros y sin telemetría.",
		q5: "Uso Wallos. ¿Puedo pasarme?", a5: "Sí. Importa tu base de datos de Wallos y revisa cada suscripción antes de guardarla.",
		q6: "¿Cómo lo desinstalo?", a6: "En un Mac, desactiva Exhale en Ajustes del Sistema → General → Ítems de inicio y arrástralo a la Papelera. En Windows, Configuración → Aplicaciones. En Linux, quita el paquete. Tus datos se quedan hasta que borres su carpeta.",
		foot: "Software libre bajo la AGPL-3.0.", "foot.docs": "Documentación", "foot.install": "Guía de instalación", "foot.security": "Seguridad",
		"foot.changelog": "Cambios", "foot.source": "Código", "foot.issues": "Incidencias",
		mine: "Para este ordenador", download: "Descargar", copied: "Copiado",
		"g.app": "App", "g.cli": "Línea de comandos", "g.deb": "Debian, Ubuntu, Raspberry Pi OS", "g.rpm": "Fedora, RHEL, openSUSE", "g.any": "Cualquier distribución",
		"a.mac": "Apple silicon e Intel", "a.silicon": "Apple silicon", "a.intel": "Intel", "a.x64": "x64", "a.amd64": "x86-64", "a.arm64": "ARM64", "a.armv7": "ARM 32 bits",
		"k.dmg": "Imagen de disco", "k.installer": "Instalador", "k.archive": "Archivo", "k.package": "Paquete",
	};

	let lang = "en";
	try { lang = localStorage.getItem("lang") || ""; } catch (e) { /* storage blocked */ }
	if (!lang) lang = (navigator.languages || [navigator.language]).some((l) => /^es\b/i.test(l)) ? "es" : "en";

	function t(key, vars) {
		let s = (lang === "es" ? es[key] : undefined) ?? en[key] ?? key;
		for (const k in vars || {}) s = s.split("{" + k + "}").join(vars[k]);
		return s;
	}

	// Remember the English in the page (as nodes, never as HTML text) before
	// translating it.
	const original = new Map();
	document.querySelectorAll("[data-t]").forEach((el) => { original.set(el, Array.from(el.childNodes, (n) => n.cloneNode(true))); });
	document.querySelectorAll("[data-t-alt]").forEach((el) => { en[el.dataset.tAlt] = el.alt; });

	function translatePage() {
		document.documentElement.lang = lang;
		document.querySelectorAll("[data-t]").forEach((el) => {
			const v = lang === "es" ? es[el.dataset.t] : undefined;
			if (v) {
				// Only the constant Spanish texts above are read as HTML.
				const tpl = document.createElement("template");
				tpl.innerHTML = v;
				el.replaceChildren(tpl.content);
			} else {
				el.replaceChildren(...original.get(el).map((n) => n.cloneNode(true)));
			}
		});
		document.querySelectorAll("[data-t-alt]").forEach((el) => { el.alt = t(el.dataset.tAlt); });
		const other = lang === "es" ? "en" : "es";
		const btn = document.getElementById("lang");
		btn.textContent = other;
		btn.setAttribute("aria-label", other === "es" ? "Español" : "English");
		btn.lang = other;
	}

	// ---- what is the visitor on ----

	function detectOS() {
		const ua = navigator.userAgent || "";
		const platform = (navigator.userAgentData && navigator.userAgentData.platform) || navigator.platform || "";
		if (/Android|iPhone|iPad|iPod/i.test(ua) || (/Mac/i.test(platform) && navigator.maxTouchPoints > 1)) return "phone";
		if (/Win/i.test(platform) || /Windows/i.test(ua)) return "windows";
		if (/Mac/i.test(platform) || /Mac OS X/i.test(ua)) return "darwin";
		if (/Linux|X11|CrOS/i.test(platform) || /Linux/i.test(ua)) return "linux";
		return "";
	}

	function detectDistro() {
		const ua = navigator.userAgent || "";
		if (/Fedora|Red Hat|CentOS|openSUSE|SUSE/i.test(ua)) return "rpm";
		if (/Ubuntu|Debian|Mint|Raspbian/i.test(ua)) return "deb";
		return "";
	}

	// Browsers hide the CPU. Chromium tells it on request; elsewhere the user
	// agent sometimes does. Unknown: x86-64.
	async function detectArch() {
		try {
			if (navigator.userAgentData && navigator.userAgentData.getHighEntropyValues) {
				const v = await navigator.userAgentData.getHighEntropyValues(["architecture", "bitness"]);
				if (v.architecture === "arm") return v.bitness === "32" ? "armv7" : "arm64";
				if (v.architecture === "x86") return "amd64";
			}
		} catch (e) { /* not allowed: fall through */ }
		const ua = navigator.userAgent || "";
		if (/aarch64|arm64/i.test(ua)) return "arm64";
		if (/armv7|armv8l|armhf/i.test(ua)) return "armv7";
		return "amd64";
	}

	// ---- rendering ----

	function el(tag, attrs, html) {
		const n = document.createElement(tag);
		for (const k in attrs || {}) n.setAttribute(k, attrs[k]);
		if (html != null) n.innerHTML = html;
		return n;
	}

	function size(bytes) {
		if (!bytes) return "";
		return bytes >= 1e6 ? (bytes / 1e6).toFixed(1) + " MB" : Math.round(bytes / 1e3) + " KB";
	}

	function downloadURL(tag, name) {
		return `https://github.com/${repo}/releases/download/${tag}/${name}`;
	}

	// release: { tag, version, date, assets: name -> {url, size} | null }
	function resolve(release, b) {
		if (release.assets) {
			const name = Object.keys(release.assets).find((n) => b.match.test(n));
			return name ? { name, ...release.assets[name] } : null;
		}
		const name = b.name(release.version);
		return { name, url: downloadURL(release.tag, name), size: 0 };
	}

	// The macOS and Windows apps fit every Mac and every Windows PC (Windows
	// on ARM runs the x64 installer). Linux needs the CPU and the package
	// format; without the format, point at the card rather than guess.
	function choose(os, arch, distro) {
		if (os === "darwin" || os === "windows") return { os, arch, build: BUILDS.find((b) => b.os === os && b.arch === "any") };
		if (os === "linux") return { os, arch, build: distro ? BUILDS.find((b) => b.os === os && b.arch === arch && b.distro === distro) || null : null };
		return null;
	}

	function renderLists(release, pick) {
		document.querySelectorAll(".platform").forEach((card) => {
			const os = card.dataset.os;
			const list = card.querySelector(".files");
			list.textContent = "";
			card.classList.toggle("mine", !!pick && pick.os === os);
			let group = "";
			for (const b of BUILDS.filter((x) => x.os === os)) {
				const file = resolve(release, b);
				if (!file) continue;
				if (b.group !== group) {
					group = b.group;
					list.appendChild(el("li", { class: "group" }, t(group)));
				}
				const mine = !!pick && pick.build === b;
				const meta = [t(b.kind) + " · " + b.ext, size(file.size)].filter(Boolean).join(" · ");
				const a = el("a", { class: "file" + (mine ? " recommended" : ""), href: file.url, download: "" },
					`<span class="what"><span class="label">${t(b.label)}${mine ? ` <span class="badge">${t("mine")}</span>` : ""}</span>` +
					`<span class="meta" title="${file.name}">${meta}</span></span><span class="get">${DOWNLOAD_ICON}</span>`);
				a.setAttribute("aria-label", `${t("download")} ${OS_NAMES[os]} ${t(b.label)} ${b.ext}${file.size ? ", " + size(file.size) : ""}`);
				const li = el("li");
				li.appendChild(a);
				list.appendChild(li);
			}
		});
	}

	const ICONS = { darwin: "#i-apple", windows: "#i-windows", linux: "#i-linux" };

	function renderHero(release, os, pick) {
		const link = document.getElementById("hero-download");
		const label = document.getElementById("hero-download-label");
		const note = document.getElementById("hero-download-note");
		document.querySelector("#hero-download-icon use").setAttribute("href", ICONS[os] || "#i-down");
		link.href = "#download";
		label.textContent = t("cta.any");
		note.innerHTML = t("note.default");
		if (os === "phone") {
			label.textContent = t("cta.phone");
			note.textContent = t("note.phone");
			return;
		}
		if (!pick) return;
		label.textContent = t("cta.for", { os: OS_NAMES[pick.os] });
		const b = pick.build;
		if (!b) {
			link.href = "#download-linux";
			note.innerHTML = t("note.linux", { v: release.version, arch: t("a." + pick.arch) });
			return;
		}
		const file = resolve(release, b);
		if (!file) return;
		link.href = file.url;
		note.innerHTML = t("note.build", {
			extra: t(b.label) + " · ", v: release.version, ext: b.ext,
			size: file.size ? " · " + size(file.size) : "", card: CARD[pick.os],
		});
	}

	function renderMeta(release) {
		const pill = document.getElementById("release-pill");
		const pillText = document.getElementById("release-pill-text");
		const meta = document.getElementById("release-meta");
		if (!release.date) {
			pillText.textContent = t("pill.default");
			meta.textContent = t("meta.default");
			return;
		}
		const d = new Date(release.date).toLocaleDateString(lang, { year: "numeric", month: "short", day: "numeric" });
		const notes = `https://github.com/${repo}/releases/tag/${release.tag}`;
		pill.href = notes;
		pillText.textContent = t("pill.release", { v: release.version }) + " · " + d;
		meta.innerHTML = t("meta.release", { v: release.version, date: t("meta.date", { d }), notes });
		document.getElementById("checksums").href = downloadURL(release.tag, "checksums.txt");
		document.getElementById("signature").href = downloadURL(release.tag, "checksums.txt.sigstore.json");
	}

	async function latest() {
		const res = await fetch(`https://api.github.com/repos/${repo}/releases/latest`, { headers: { Accept: "application/vnd.github+json" } });
		if (!res.ok) throw new Error(String(res.status));
		const rel = await res.json();
		const assets = {};
		for (const a of rel.assets || []) assets[a.name] = { url: a.browser_download_url, size: a.size };
		return { tag: rel.tag_name, version: rel.tag_name.replace(/^v/, ""), date: rel.published_at, assets };
	}

	// ---- install tabs and copy buttons ----

	function selectTab(name, focus) {
		document.querySelectorAll(".tabs button").forEach((b) => {
			const on = b.dataset.tab === name;
			b.setAttribute("aria-selected", String(on));
			b.tabIndex = on ? 0 : -1;
			if (on && focus) b.focus();
		});
		document.querySelectorAll(".panel").forEach((p) => { p.hidden = p.dataset.tab !== name; });
	}

	function wireTabs(os) {
		const tabs = Array.from(document.querySelectorAll(".tabs button"));
		tabs.forEach((b, i) => {
			b.addEventListener("click", () => selectTab(b.dataset.tab));
			b.addEventListener("keydown", (e) => {
				if (e.key !== "ArrowRight" && e.key !== "ArrowLeft") return;
				const next = tabs[(i + (e.key === "ArrowRight" ? 1 : tabs.length - 1)) % tabs.length];
				selectTab(next.dataset.tab, true);
			});
		});
		selectTab(os === "linux" ? "linux" : "docker");
		document.querySelectorAll(".copy").forEach((btn) => {
			btn.addEventListener("click", async () => {
				const text = btn.parentElement.querySelector("code").textContent;
				try {
					await navigator.clipboard.writeText(text);
					btn.textContent = t("copied");
					btn.classList.add("done");
					setTimeout(() => { btn.textContent = t("copy"); btn.classList.remove("done"); }, 1600);
				} catch (e) { /* clipboard blocked: the command stays selectable */ }
			});
		});
	}

	// ---- start ----

	async function main() {
		const os = detectOS();
		const desktop = os && os !== "phone";
		let release = { tag: "v" + VERSION, version: VERSION, date: "", assets: null };
		let pick = desktop ? choose(os, "amd64", detectDistro()) : null;
		const render = () => {
			translatePage();
			renderLists(release, pick);
			renderHero(release, os, pick);
			renderMeta(release);
		};

		wireTabs(os);
		document.getElementById("lang").addEventListener("click", () => {
			lang = lang === "es" ? "en" : "es";
			try { localStorage.setItem("lang", lang); } catch (e) { /* storage blocked */ }
			render();
		});
		const top = document.querySelector(".top");
		addEventListener("scroll", () => top.classList.toggle("scrolled", scrollY > 8), { passive: true });
		render();

		const [arch, fresh] = await Promise.all([
			desktop ? detectArch() : Promise.resolve(""),
			latest().catch(() => null), // offline or rate-limited: keep VERSION's links
		]);
		if (fresh) release = fresh;
		if (desktop) pick = choose(os, arch, detectDistro());
		render();
	}

	main();
})();
