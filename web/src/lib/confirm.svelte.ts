// In-app replacement for window.confirm, native dialogs are suppressed in
// some embedded browsers, which silently cancels destructive actions.
export const confirmState = $state({
	open: false,
	message: '',
	resolve: null as null | ((v: boolean) => void)
});

export function askConfirm(message: string): Promise<boolean> {
	confirmState.message = message;
	confirmState.open = true;
	return new Promise((res) => (confirmState.resolve = res));
}

export function answerConfirm(v: boolean) {
	confirmState.open = false;
	confirmState.resolve?.(v);
	confirmState.resolve = null;
}
