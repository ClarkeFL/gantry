<script lang="ts">
	import { toast } from 'svelte-sonner';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import { Switch } from '$lib/components/ui/switch';
	import PlusIcon from '@lucide/svelte/icons/plus';
	import UploadIcon from '@lucide/svelte/icons/upload';
	import Trash2Icon from '@lucide/svelte/icons/trash-2';

	let {
		env,
		inheritedEnv = {},
		restartLabel = 'Restart app',
		onsave
	}: {
		env: Record<string, string>;
		// project-provided values; matching keys get a "project" badge,
		// differing values an "override" badge
		inheritedEnv?: Record<string, string>;
		restartLabel?: string;
		onsave: (set: Record<string, string>, unset: string[], restart: boolean) => Promise<void>;
	} = $props();

	let rows = $state<{ key: string; value: string }[]>([]);
	let origEnv: Record<string, string> = {};
	let restartAfterSave = $state(true);
	let saving = $state(false);
	const fileId = 'env-file-' + Math.random().toString(36).slice(2);

	// rebuild rows whenever the parent reloads env (same behavior as the old
	// inline editor: a reload discards unsaved edits)
	$effect(() => {
		rows = Object.entries(env).map(([key, value]) => ({ key, value }));
		origEnv = { ...env };
	});

	function importEnvFile(e: Event) {
		const file = (e.target as HTMLInputElement).files?.[0];
		if (!file) return;
		file.text().then((text) => {
			let count = 0;
			for (let line of text.split('\n')) {
				line = line.trim();
				if (!line || line.startsWith('#')) continue;
				line = line.replace(/^export\s+/, '');
				const eq = line.indexOf('=');
				if (eq < 1) continue;
				const key = line.slice(0, eq).trim();
				let value = line.slice(eq + 1).trim();
				if ((value.startsWith('"') && value.endsWith('"')) || (value.startsWith("'") && value.endsWith("'"))) {
					value = value.slice(1, -1);
				}
				const existing = rows.find((r) => r.key === key);
				if (existing) existing.value = value;
				else rows.push({ key, value });
				count++;
			}
			toast.success(`Imported ${count} variables, review below, then Save changes`);
		});
		(e.target as HTMLInputElement).value = '';
	}

	async function save() {
		const set: Record<string, string> = {};
		const seen = new Set<string>();
		for (const r of rows) {
			const k = r.key.trim();
			if (!k) continue;
			if (seen.has(k)) {
				toast.error('Duplicate key: ' + k);
				return;
			}
			seen.add(k);
			if (origEnv[k] !== r.value) set[k] = r.value;
		}
		const unset = Object.keys(origEnv).filter((k) => !seen.has(k));
		if (!Object.keys(set).length && !unset.length) {
			toast.info('Nothing changed');
			return;
		}
		saving = true;
		try {
			await onsave(set, unset, restartAfterSave);
		} finally {
			saving = false;
		}
	}
</script>

<div class="grid gap-2">
	{#each rows as row, i (i)}
		<div class="flex items-center gap-2">
			<Input class="w-56 font-mono text-xs" placeholder="KEY" bind:value={row.key} />
			<Input class="flex-1 font-mono text-xs" placeholder="value" bind:value={row.value} />
			{#if row.key in inheritedEnv}
				{#if inheritedEnv[row.key] === row.value}
					<Badge variant="secondary" class="shrink-0" title="Inherited from the project's shared variables">
						project
					</Badge>
				{:else}
					<Badge
						class="shrink-0 border-transparent bg-amber-500/15 text-amber-500"
						title="This app's own value; project changes to this key won't touch it"
					>
						override
					</Badge>
				{/if}
			{/if}
			<Button
				variant="ghost"
				size="icon"
				onclick={() => (rows = rows.filter((_, j) => j !== i))}
				aria-label="Remove variable"
			>
				<Trash2Icon class="size-4" />
			</Button>
		</div>
	{:else}
		<p class="text-muted-foreground text-sm">No variables set.</p>
	{/each}
	<div class="mt-2 flex flex-wrap items-center gap-2">
		<Button variant="outline" size="sm" onclick={() => rows.push({ key: '', value: '' })}>
			<PlusIcon class="size-4" /> Add variable
		</Button>
		<Button variant="outline" size="sm" onclick={() => document.getElementById(fileId)?.click()}>
			<UploadIcon class="size-4" /> Import .env
		</Button>
		<input id={fileId} type="file" accept=".env,text/plain,.txt" class="hidden" onchange={importEnvFile} />
		<div class="ml-auto flex items-center gap-2">
			<Switch id="{fileId}-restart" bind:checked={restartAfterSave} />
			<Label for="{fileId}-restart" class="text-sm whitespace-nowrap">{restartLabel}</Label>
		</div>
		<Button size="sm" onclick={save} disabled={saving}>
			{saving ? 'Saving…' : 'Save changes'}
		</Button>
	</div>
</div>
