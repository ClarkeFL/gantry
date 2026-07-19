<script lang="ts">
	import { onMount } from 'svelte';
	import { api, stream } from '$lib/api';
	import { toast } from 'svelte-sonner';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import * as Card from '$lib/components/ui/card';
	import * as Dialog from '$lib/components/ui/dialog';
	import DatabaseIcon from '@lucide/svelte/icons/database';
	import CloudUploadIcon from '@lucide/svelte/icons/cloud-upload';
	import BoxIcon from '@lucide/svelte/icons/box';
	import DownloadIcon from '@lucide/svelte/icons/download';
	import CronInput from '$lib/components/cron-input.svelte';
	import { fmtDate } from '$lib/dates';
	import ArchiveRestoreIcon from '@lucide/svelte/icons/archive-restore';

	type Db = { type: string; name: string; schedule: string };

	let dbs = $state<Db[]>([]);
	let appNames = $state<string[]>([]);
	let s3Set = $state(false);
	let bucket = $state('');
	let loading = $state(true);

	// S3 form
	let s3Bucket = $state('');
	let s3Region = $state('');
	let s3Key = $state('');
	let s3Secret = $state('');
	let s3Endpoint = $state('');
	let savingS3 = $state(false);

	// per-row schedule edits
	let schedules = $state<Record<string, string>>({});

	// server backup
	let serverSchedule = $state('');
	let serverKeep = $state(7);
	let lastBackup = $state('');
	let savingServer = $state(false);

	const lastParsed = $derived.by(() => {
		if (!lastBackup) return null;
		const m = lastBackup.match(/^(\S+)\s+(ok|failed:?)\s*(.*)$/);
		if (!m) return { when: lastBackup, ok: null as boolean | null, detail: '' };
		const size = m[3].match(/\((\d+) KB/)?.[1];
		return {
			when: fmtDate(m[1]),
			ok: m[2] === 'ok',
			detail: m[2] === 'ok' ? (size ? `${size} KB` : '') : m[3]
		};
	});

	async function saveServerSchedule(e: SubmitEvent) {
		e.preventDefault();
		savingServer = true;
		try {
			await api('/backup/server/schedule', {
				method: 'POST',
				body: JSON.stringify({ schedule: serverSchedule.trim(), keep: serverKeep })
			});
			toast.success(
				serverSchedule.trim()
					? `Server backup scheduled: ${serverSchedule.trim()}, keeping last ${serverKeep}`
					: 'Server backup schedule removed'
			);
			await load();
		} catch (e) {
			toast.error(msg(e));
		} finally {
			savingServer = false;
		}
	}

	function serverBackupNow() {
		backupTarget = 'server';
		backupLines = [];
		backupOpen = true;
		backingUp = true;
		stream(
			'/backup/server',
			(l) => {
				backupLines.push(l);
			},
			{ method: 'POST' },
			async () => {
				backingUp = false;
				await load();
			}
		);
	}

	// backup-now dialog
	let backupOpen = $state(false);
	let backupTarget = $state('');
	let backupLines = $state<string[]>([]);
	let backingUp = $state(false);

	function msg(e: unknown) {
		return e instanceof Error ? e.message : String(e);
	}

	async function load() {
		loading = true;
		try {
			const d = await api('/backups');
			dbs = d.databases ?? [];
			s3Set = d.s3Set;
			bucket = d.bucket ?? '';
			serverSchedule = d.serverSchedule ?? '';
			serverKeep = d.serverKeep ?? 7;
			lastBackup = d.lastBackup ?? '';
			for (const db of dbs) schedules[db.type + '/' + db.name] = db.schedule;
			const s = await api('/settings');
			s3Bucket = s.s3.bucket ?? '';
			s3Region = s.s3.region ?? '';
			s3Endpoint = s.s3.endpoint ?? '';
			const a = await api('/apps');
			appNames = (a.apps ?? []).map((x: { name: string }) => x.name);
		} finally {
			loading = false;
		}
	}
	onMount(load);

	async function saveS3(e: SubmitEvent) {
		e.preventDefault();
		savingS3 = true;
		try {
			await api('/settings/s3', {
				method: 'POST',
				body: JSON.stringify({
					bucket: s3Bucket,
					region: s3Region,
					key: s3Key,
					secret: s3Secret,
					endpoint: s3Endpoint
				})
			});
			toast.success('S3 storage saved');
			s3Key = s3Secret = '';
			await load();
		} catch (e) {
			toast.error(msg(e));
		} finally {
			savingS3 = false;
		}
	}

	// schedule changes save themselves, debounced, with a quiet inline indicator
	let saveState = $state<Record<string, string>>({});
	const saveTimers: Record<string, ReturnType<typeof setTimeout>> = {};

	function scheduleChanged(db: Db) {
		const key = db.type + '/' + db.name;
		clearTimeout(saveTimers[key]);
		saveTimers[key] = setTimeout(async () => {
			saveState[key] = 'saving';
			try {
				await api('/services/backup/schedule', {
					method: 'POST',
					body: JSON.stringify({ type: db.type, name: db.name, schedule: (schedules[key] ?? '').trim() })
				});
				saveState[key] = 'saved';
				setTimeout(() => {
					if (saveState[key] === 'saved') saveState[key] = '';
				}, 2500);
			} catch (e) {
				saveState[key] = '';
				toast.error(msg(e));
			}
		}, 700);
	}

	// restore-an-app dialog
	let restoreOpen = $state(false);
	let restoreKeys = $state<string[]>([]);
	let restoreKey = $state('');
	let restoreApps = $state<string[]>([]);
	let restoreApp = $state('');
	let restoreDef = $state<{ name: string } | null>(null);
	let restoring = $state(false);

	async function openRestore() {
		restoreDef = null;
		restoreOpen = true;
		try {
			const d = await api('/backup/list');
			restoreKeys = d.keys ?? [];
			if (restoreKeys.length) {
				restoreKey = restoreKeys[0];
				await loadArchiveApps();
			}
		} catch (e) {
			toast.error(msg(e));
		}
	}

	async function loadArchiveApps() {
		restoreApps = [];
		try {
			const d = await api('/backup/apps?key=' + encodeURIComponent(restoreKey));
			restoreApps = d.apps ?? [];
			restoreApp = restoreApps[0] ?? '';
		} catch (e) {
			toast.error(msg(e));
		}
	}

	function onRestoreFile(e: Event) {
		const file = (e.target as HTMLInputElement).files?.[0];
		if (!file) return;
		file.text().then((text) => {
			try {
				const def = JSON.parse(text);
				if (!def.name) throw new Error('not a gantry app definition');
				restoreDef = def;
			} catch {
				toast.error('That file is not a gantry app definition');
			}
		});
	}

	async function doRestore() {
		const name = restoreDef?.name ?? restoreApp;
		if (!name) return;
		restoring = true;
		try {
			const res = await api(`/apps/${name}/restore`, {
				method: 'POST',
				body: JSON.stringify(restoreDef ? { def: restoreDef } : { key: restoreKey })
			});
			toast.success(
				`Restored ${name}: ${res.env} env vars, ${res.domains} domains. Open the app and press Deploy to rebuild it.`,
				{ duration: 8000 }
			);
			restoreOpen = false;
			await load();
		} catch (e) {
			toast.error(msg(e));
		} finally {
			restoring = false;
		}
	}

	async function exportApp(name: string) {
		try {
			const d = await api('/apps/' + name);
			const def = {
				name,
				env: d.env,
				domains: (d.domains ?? []).map((x: { name: string }) => x.name),
				repo: d.repo,
				ref: d.ref,
				buildDir: d.buildDir,
				dockerfile: d.dockerfile,
				image: d.image,
				cron: d.jobs,
				category: d.category
			};
			const url = URL.createObjectURL(
				new Blob([JSON.stringify(def, null, 2)], { type: 'application/json' })
			);
			const a = document.createElement('a');
			a.href = url;
			a.download = `gantry-app-${name}.json`;
			a.click();
			URL.revokeObjectURL(url);
		} catch (e) {
			toast.error(msg(e));
		}
	}

	function backupNow(db: Db) {
		backupTarget = `${db.type}/${db.name}`;
		backupLines = [];
		backupOpen = true;
		backingUp = true;
		stream(
			'/services/backup',
			(l) => {
				backupLines.push(l);
			},
			{ method: 'POST', body: JSON.stringify({ type: db.type, name: db.name }) },
			() => {
				backingUp = false;
			}
		);
	}
</script>

<div class="mx-auto grid max-w-4xl gap-6">
	<h1 class="text-2xl font-semibold tracking-tight">Backups</h1>

	<Card.Root>
		<Card.Header>
			<Card.Title class="flex items-center gap-2 text-base">
				S3 storage
				{#if s3Set}
					<span class="rounded bg-emerald-500/15 px-1.5 py-0.5 text-xs font-medium text-emerald-500">configured</span>
				{:else}
					<span class="rounded bg-amber-500/15 px-1.5 py-0.5 text-xs font-medium text-amber-500">not set</span>
				{/if}
			</Card.Title>
			<Card.Description>
				Where database dumps go. Works with AWS S3 or any S3-compatible storage (Backblaze B2,
				Wasabi, MinIO…) via the endpoint field.
			</Card.Description>
		</Card.Header>
		<Card.Content>
			<form onsubmit={saveS3} class="grid max-w-lg gap-3">
				<div class="grid grid-cols-2 gap-3">
					<div class="grid gap-1">
						<Label for="s3-bucket">Bucket</Label>
						<Input id="s3-bucket" bind:value={s3Bucket} required placeholder="my-backups" />
					</div>
					<div class="grid gap-1">
						<Label for="s3-region">Region</Label>
						<Input id="s3-region" bind:value={s3Region} placeholder="eu-west-2" />
					</div>
				</div>
				<div class="grid grid-cols-2 gap-3">
					<div class="grid gap-1">
						<Label for="s3-key">Access key {#if s3Set}<span class="text-muted-foreground">(saved, blank keeps it)</span>{/if}</Label>
						<Input id="s3-key" bind:value={s3Key} placeholder="AKIA…" autocomplete="off" />
					</div>
					<div class="grid gap-1">
						<Label for="s3-secret">Secret key</Label>
						<Input id="s3-secret" type="password" bind:value={s3Secret} autocomplete="off" />
					</div>
				</div>
				<div class="grid gap-1">
					<Label for="s3-endpoint">Endpoint <span class="text-muted-foreground">(optional, non-AWS only)</span></Label>
					<Input id="s3-endpoint" bind:value={s3Endpoint} placeholder="https://s3.eu-central-003.backblazeb2.com" />
				</div>
				<Button type="submit" class="w-fit" disabled={savingS3}>{savingS3 ? 'Saving…' : 'Save'}</Button>
			</form>
		</Card.Content>
	</Card.Root>

	<Card.Root>
		<Card.Header>
			<Card.Title class="text-base">Server backup</Card.Title>
			<Card.Description>
				A complete snapshot of how this server is set up: panel settings, plus every app's deploy
				source, environment variables, domains and scheduled jobs. If this server is ever lost,
				install gantry on a new one and bring everything back with one command
				(<code>gantry restore &lt;file&gt;</code>).
			</Card.Description>
		</Card.Header>
		<Card.Content class="grid gap-3">
			<form onsubmit={saveServerSchedule} class="grid gap-3">
				<div class="grid gap-1.5">
					<Label>Schedule</Label>
					<CronInput bind:value={serverSchedule} allowEmpty />
				</div>
				<div class="flex flex-wrap items-center gap-3">
					<Label for="srv-keep">Keep last</Label>
					<Input id="srv-keep" type="number" min={1} max={100} bind:value={serverKeep} class="w-20" />
					<span class="text-muted-foreground text-sm">backups, older ones are deleted</span>
					<Button type="submit" variant="outline" disabled={savingServer || !s3Set}>Save</Button>
					<Button type="button" onclick={serverBackupNow} disabled={!s3Set}>
						<CloudUploadIcon class="size-4" /> Backup now
					</Button>
				</div>
			</form>
			{#if lastParsed}
				<p class="text-muted-foreground flex items-center gap-1.5 text-xs">
					Last backup: {lastParsed.when}
					{#if lastParsed.ok === true}
						<span class="text-emerald-500">succeeded</span>{#if lastParsed.detail}<span>({lastParsed.detail})</span>{/if}
					{:else if lastParsed.ok === false}
						<span class="text-red-500">failed</span> {lastParsed.detail}
					{/if}
				</p>
			{/if}
			{#if !s3Set}
				<p class="text-muted-foreground text-sm">Configure S3 storage above first.</p>
			{/if}
		</Card.Content>
	</Card.Root>

	<Card.Root>
		<Card.Header>
			<Card.Title class="text-base">App backups</Card.Title>
			<Card.Description>
				Each app's setup is saved inside every server backup above, and its code lives in its
				GitHub repo or Docker image, so a restore has everything it needs. Use Restore an app to
				bring back a single app (for example after deleting one by mistake) without touching the
				rest of the server, or download an app's setup as a file to keep.
			</Card.Description>
		</Card.Header>
		<Card.Content class="grid gap-2">
			<div>
				<Button variant="outline" size="sm" onclick={openRestore}>
					<ArchiveRestoreIcon class="size-4" /> Restore an app
				</Button>
			</div>
			{#if !appNames.length && !loading}
				<p class="text-muted-foreground text-sm">No apps yet.</p>
			{/if}
			{#each appNames as name (name)}
				<div class="flex items-center gap-3 rounded-md border px-3 py-2">
					<BoxIcon class="text-muted-foreground size-4" />
					<span class="text-sm font-medium">{name}</span>
					<Badge variant="outline" class="text-xs">in server backup</Badge>
					<Button size="sm" variant="outline" class="ml-auto" onclick={() => exportApp(name)}>
						<DownloadIcon class="size-4" /> Download definition
					</Button>
				</div>
			{/each}
		</Card.Content>
	</Card.Root>

	<Card.Root>
		<Card.Header>
			<Card.Title class="text-base">Database backups</Card.Title>
			<Card.Description>
				Saves a copy of each database's data to your storage. Use Backup now for a one-off copy,
				or set a schedule and it happens automatically.
			</Card.Description>
		</Card.Header>
		<Card.Content class="grid gap-2">
			{#if !dbs.length && !loading}
				<p class="text-muted-foreground text-sm">No databases yet, create one on the Databases page.</p>
			{/if}
			{#each dbs as db (db.type + db.name)}
				{@const key = db.type + '/' + db.name}
				<div class="grid gap-2.5 rounded-md border p-3">
					<div class="flex items-center gap-2">
						<DatabaseIcon class="text-muted-foreground size-4" />
						<span class="text-sm font-medium">{db.name}</span>
						<Badge variant="secondary">{db.type}</Badge>
						<span class="ml-auto text-xs {saveState[key] === 'saved' ? 'text-emerald-500' : 'text-muted-foreground'}">
							{saveState[key] === 'saving' ? 'Saving…' : saveState[key] === 'saved' ? 'Schedule saved' : ''}
						</span>
						<Button size="sm" onclick={() => backupNow(db)} disabled={!s3Set}>
							<CloudUploadIcon class="size-4" /> Backup now
						</Button>
					</div>
					{#if s3Set}
						<CronInput compact bind:value={schedules[key]} allowEmpty onchange={() => scheduleChanged(db)} />
					{/if}
				</div>
			{/each}
			{#if !s3Set && dbs.length}
				<p class="text-muted-foreground text-sm">Configure S3 storage above to enable backups.</p>
			{/if}
		</Card.Content>
	</Card.Root>
</div>

<Dialog.Root bind:open={restoreOpen}>
	<Dialog.Content class="max-w-md">
		<Dialog.Header>
			<Dialog.Title>Restore an app</Dialog.Title>
			<Dialog.Description>
				Pick a server backup and choose which app to bring back, or upload an app file you
				downloaded earlier. Only that app is changed.
			</Dialog.Description>
		</Dialog.Header>
		<div class="grid gap-4">
			{#if !restoreDef}
				<div class="grid gap-1.5">
					<Label>From backup</Label>
					<select
						class="border-input dark:bg-input/30 h-9 rounded-md border bg-transparent px-2.5 text-sm"
						bind:value={restoreKey}
						onchange={loadArchiveApps}
					>
						{#each restoreKeys as k (k)}<option value={k}>{k.replace('gantry/', '')}</option>{/each}
					</select>
				</div>
				<div class="grid gap-1.5">
					<Label>App to restore</Label>
					<select
						class="border-input dark:bg-input/30 h-9 rounded-md border bg-transparent px-2.5 text-sm"
						bind:value={restoreApp}
					>
						{#each restoreApps as a (a)}<option value={a}>{a}</option>{/each}
					</select>
				</div>
			{:else}
				<p class="text-sm">
					From file: restoring <code class="text-foreground">{restoreDef.name}</code>
					<Button variant="ghost" size="sm" onclick={() => (restoreDef = null)}>Use a backup instead</Button>
				</p>
			{/if}
			<div class="grid gap-1.5">
				<Label for="restore-file">Or upload an app file</Label>
				<input id="restore-file" type="file" accept=".json" onchange={onRestoreFile} class="text-sm" />
			</div>
			<Button onclick={doRestore} disabled={restoring || (!restoreDef && !restoreApp)}>
				{restoring ? 'Restoring…' : 'Restore'}
			</Button>
		</div>
	</Dialog.Content>
</Dialog.Root>

<Dialog.Root bind:open={backupOpen}>
	<Dialog.Content class="max-w-xl">
		<Dialog.Header>
			<Dialog.Title>Backing up {backupTarget}</Dialog.Title>
		</Dialog.Header>
		<div class="bg-card max-h-72 overflow-y-auto rounded-md border p-3 font-mono text-xs leading-5">
			{#each backupLines as line, i (i)}<div class="whitespace-pre-wrap">{line}</div>{/each}
			{#if backingUp}<div class="text-muted-foreground">…</div>{/if}
		</div>
	</Dialog.Content>
</Dialog.Root>
