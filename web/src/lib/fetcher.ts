// Fetcher used by the generated client. Errors follow RFC 9457 (huma).
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
		throw new ApiError(res.status, body?.detail ?? res.statusText, body?.errors ?? []);
	}
	return body as T;
};
