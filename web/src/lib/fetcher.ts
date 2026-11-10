// Fetcher used by the generated client. Errors follow RFC 9457 (huma); a
// problem that carries a text is shown in this browser's language.
import { capitalize, type ServerText, say } from "@/lib/i18n";

/** The detail of a problem, translated when the server sent its text. */
export function problemDetail(
	body: { detail?: string; text?: ServerText } | undefined,
	fallback: string,
): string {
	if (body?.text) return capitalize(say(body.text));
	return body?.detail ?? fallback;
}

export class ApiError extends Error {
	constructor(
		public status: number,
		public detail: string,
		public errors: { location?: string; message?: string }[] = [],
	) {
		super(detail);
	}
}

export const fetcher = async <T>(url: string, options?: RequestInit): Promise<T> => {
	const res = await fetch(url, {
		...options,
		headers: { "Content-Type": "application/json", ...options?.headers },
	});
	const text = await res.text();
	const body = text ? JSON.parse(text) : undefined;
	if (!res.ok) {
		throw new ApiError(res.status, problemDetail(body, res.statusText), body?.errors ?? []);
	}
	return body as T;
};
