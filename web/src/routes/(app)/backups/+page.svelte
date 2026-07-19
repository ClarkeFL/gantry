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

	async function saveSchedule(db: Db) {
		const schedule = (schedules[db.type + '/' + db.name] ?? '').trim();
		try {
			await api('/services/backup/schedule', {
				method: 'POST',
				body: JSON.stringify({ type: db.type, name: db.name, schedule })
			});
			toast.success(schedule ? `Scheduled ${db.name}: ${schedule}` : `Schedule removed for ${db.name}`);
			await load();
		} catch (e) {
			toast.error(msg(e));
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
				One archive of everything that defines this server: panel settings, app sources, env vars,
				domains, cron jobs and categories. Restore onto a fresh install with
				<code>gantry restore &lt;file&gt;</code>. Old backups beyond the keep count are deleted
				automatically.
			</Card.Description>
		</Card.Header>
		<Card.Content class="grid gap-3">
			<form onsubmit={saveServerSchedule} class="flex flex-wrap items-end gap-3">
				<div class="grid gap-1">
					<Label>Schedule</Label>
					<CronInput bind:value={serverSchedule} allowEmpty />
				</div>
				<div class="grid gap-1">
					<Label for="srv-keep">Keep last</Label>
					<Input id="srv-keep" type="number" min={1} max={100} bind:value={serverKeep} class="w-20" />
				</div>
				<Button type="submit" variant="outline" disabled={savingServer || !s3Set}>Save</Button>
				<Button type="button" onclick={serverBackupNow} disabled={!s3Set}>
					<CloudUploadIcon class="size-4" /> Backup now
				</Button>
			</form>
			{#if lastBackup}
				<p class="text-muted-foreground font-mono text-xs">last: {lastBackup}</p>
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
				Every server backup above already includes each app's full definition, deploy source,
				environment variables, domains, cron jobs and category. The app itself rebuilds from its
				repo or image on deploy, so that definition is all a restore needs. You can also download
				a single app's definition here.
			</Card.Description>
		</Card.Header>
		<Card.Content class="grid gap-2">
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
				Backup now, or pick how often each database is dumped to
				<code>s3://{bucket || 'your-bucket'}</code> automatically. Restore with
				<code>dokku &lt;type&gt;:import &lt;name&gt; &lt; dump.sql</code>.
			</Card.Description>
		</Card.Header>
		<Card.Content class="grid gap-2">
			{#if !dbs.length && !loading}
				<p class="text-muted-foreground text-sm">No databases yet, create one on the Databases page.</p>
			{/if}
			{#each dbs as db (db.type + db.name)}
				<div class="flex flex-wrap items-center gap-3 rounded-md border px-3 py-2">
					<DatabaseIcon class="text-muted-foreground size-4" />
					<span class="text-sm font-medium">{db.name}</span>
					<Badge variant="secondary">{db.type}</Badge>
					{#if db.schedule}
						<Badge variant="outline" class="font-mono text-xs">{db.schedule}</Badge>
					{/if}
					<div class="ml-auto flex flex-wrap items-start gap-2">
						<CronInput bind:value={schedules[db.type + '/' + db.name]} allowEmpty />
						<Button size="sm" variant="outline" onclick={() => saveSchedule(db)} disabled={!s3Set}>
							Save schedule
						</Button>
						<Button size="sm" onclick={() => backupNow(db)} disabled={!s3Set}>
							<CloudUploadIcon class="size-4" /> Backup now
						</Button>
					</div>
				</div>
			{/each}
			{#if !s3Set && dbs.length}
				<p class="text-muted-foreground text-sm">Configure S3 storage above to enable backups.</p>
			{/if}
		</Card.Content>
	</Card.Root>
</div>

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
