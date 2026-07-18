<script lang="ts">
	import { onDestroy, onMount } from 'svelte';
	import { page } from '$app/state';
	import { api, stream } from '$lib/api';
	import { toast } from 'svelte-sonner';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import { Switch } from '$lib/components/ui/switch';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import * as Card from '$lib/components/ui/card';
	import * as Tabs from '$lib/components/ui/tabs';
	import * as Dialog from '$lib/components/ui/dialog';
	import ArrowLeftIcon from '@lucide/svelte/icons/arrow-left';
	import RotateCwIcon from '@lucide/svelte/icons/rotate-cw';
	import SquareIcon from '@lucide/svelte/icons/square';
	import PlayIcon from '@lucide/svelte/icons/play';
	import RocketIcon from '@lucide/svelte/icons/rocket';
	import PlusIcon from '@lucide/svelte/icons/plus';
	import Trash2Icon from '@lucide/svelte/icons/trash-2';

	type Job = { id: string; schedule: string; command: string; last?: string };
	type Detail = {
		name: string;
		running: boolean;
		category: string;
		env: Record<string, string>;
		domains: string[];
		jobs: Job[];
		nativeCron: string;
	};

	const name = $derived(page.params.name!);

	let d = $state<Detail | null>(null);
	let tab = $state('overview');

	// env editor
	let rows = $state<{ key: string; value: string }[]>([]);
	let origEnv: Record<string, string> = {};
	let restartAfterSave = $state(true);
	let savingEnv = $state(false);

	// cron editor
	let jobs = $state<Job[]>([]);
	let savingCron = $state(false);

	// category
	let category = $state('');
	let savingCategory = $state(false);

	// logs
	let logLines = $state<string[]>([]);
	let logEl = $state<HTMLElement | null>(null);
	let stopLogs: (() => void) | null = null;

	// deploy
	let deployOpen = $state(false);
	let deploying = $state(false);
	let deployLines = $state<string[]>([]);
	let image = $state('');
	let stopDeploy: (() => void) | null = null;

	function msg(e: unknown) {
		return e instanceof Error ? e.message : String(e);
	}

	async function load() {
		d = await api<Detail>('/apps/' + name);
		rows = Object.entries(d.env).map(([key, value]) => ({ key, value }));
		origEnv = { ...d.env };
		jobs = d.jobs.map((j) => ({ ...j }));
		category = d.category;
	}
	onMount(() => {
		load().catch((e) => toast.error(msg(e)));
	});
	onDestroy(() => {
		stopLogs?.();
		stopDeploy?.();
	});

	$effect(() => {
		if (tab === 'logs' && !stopLogs) {
			logLines = [];
			stopLogs = stream(`/apps/${name}/logs`, (l) => {
				logLines.push(l);
			});
		} else if (tab !== 'logs' && stopLogs) {
			stopLogs();
			stopLogs = null;
		}
	});
	$effect(() => {
		if (logLines.length && logEl) logEl.scrollTop = logEl.scrollHeight;
	});

	async function ps(action: 'restart' | 'stop' | 'start') {
		try {
			await api(`/apps/${name}/ps`, { method: 'POST', body: JSON.stringify({ action }) });
			toast.success(`${action}: ok`);
			await load();
		} catch (e) {
			toast.error(msg(e));
		}
	}

	function deploy() {
		deploying = true;
		deployLines = [];
		stopDeploy = stream(
			`/apps/${name}/deploy`,
			(l) => {
				deployLines.push(l);
			},
			{ method: 'POST', body: JSON.stringify({ image: image.trim() }) },
			() => {
				deploying = false;
				load();
			}
		);
	}

	async function saveEnv() {
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
		savingEnv = true;
		try {
			await api(`/apps/${name}/env`, {
				method: 'POST',
				body: JSON.stringify({ set, unset, restart: restartAfterSave })
			});
			toast.success('Environment saved' + (restartAfterSave ? ' — app restarting' : ''));
			await load();
		} catch (e) {
			toast.error(msg(e));
		} finally {
			savingEnv = false;
		}
	}

	async function saveCron() {
		savingCron = true;
		try {
			const res = await api<{ jobs: Job[] }>(`/apps/${name}/cron`, {
				method: 'PUT',
				body: JSON.stringify({ jobs: jobs.map(({ id, schedule, command }) => ({ id, schedule, command })) })
			});
			jobs = res.jobs;
			toast.success('Cron jobs saved');
		} catch (e) {
			toast.error(msg(e));
		} finally {
			savingCron = false;
		}
	}

	async function saveCategory() {
		savingCategory = true;
		try {
			await api(`/apps/${name}/category`, { method: 'POST', body: JSON.stringify({ category }) });
			toast.success('Category saved');
		} catch (e) {
			toast.error(msg(e));
		} finally {
			savingCategory = false;
		}
	}

	function lastBadge(last?: string): { label: string; ok: boolean } | null {
		if (!last) return null;
		const [ts] = last.split(' ');
		return { label: `${last.includes('exit=0') ? 'ok' : 'failed'} · ${ts.replace('T', ' ').slice(0, 16)}`, ok: last.includes('exit=0') };
	}
</script>

<div class="mx-auto max-w-4xl">
	<a href="/apps" class="text-muted-foreground hover:text-foreground mb-4 inline-flex items-center gap-1 text-sm">
		<ArrowLeftIcon class="size-4" /> Apps
	</a>

	{#if !d}
		<Skeleton class="h-64" />
	{:else}
		<div class="mb-6 flex flex-wrap items-center gap-3">
			<h1 class="text-2xl font-semibold tracking-tight">{d.name}</h1>
			<Badge variant={d.running ? 'default' : 'destructive'}>{d.running ? 'running' : 'stopped'}</Badge>
			<div class="ml-auto flex gap-2">
				{#if d.running}
					<Button variant="outline" size="sm" onclick={() => ps('restart')}>
						<RotateCwIcon class="size-4" /> Restart
					</Button>
					<Button variant="outline" size="sm" onclick={() => ps('stop')}>
						<SquareIcon class="size-4" /> Stop
					</Button>
				{:else}
					<Button variant="outline" size="sm" onclick={() => ps('start')}>
						<PlayIcon class="size-4" /> Start
					</Button>
				{/if}
				<Button size="sm" onclick={() => (deployOpen = true)}>
					<RocketIcon class="size-4" /> Deploy
				</Button>
			</div>
		</div>

		<Tabs.Root bind:value={tab}>
			<Tabs.List>
				<Tabs.Trigger value="overview">Overview</Tabs.Trigger>
				<Tabs.Trigger value="env">Environment</Tabs.Trigger>
				<Tabs.Trigger value="cron">Cron</Tabs.Trigger>
				<Tabs.Trigger value="logs">Logs</Tabs.Trigger>
			</Tabs.List>

			<Tabs.Content value="overview" class="mt-4 grid gap-4">
				<Card.Root>
					<Card.Header>
						<Card.Title class="text-base">Domains</Card.Title>
						<Card.Description>Managed via <code>dokku domains</code> / <code>dokku letsencrypt</code> for now.</Card.Description>
					</Card.Header>
					<Card.Content class="flex flex-wrap gap-2">
						{#each d.domains as domain (domain)}
							<a href="https://{domain}" target="_blank" rel="noreferrer">
								<Badge variant="outline">{domain}</Badge>
							</a>
						{:else}
							<p class="text-muted-foreground text-sm">No domains configured.</p>
						{/each}
					</Card.Content>
				</Card.Root>

				<Card.Root>
					<Card.Header>
						<Card.Title class="text-base">Category</Card.Title>
						<Card.Description>Groups this app on the dashboard.</Card.Description>
					</Card.Header>
					<Card.Content class="flex max-w-sm gap-2">
						<Input bind:value={category} placeholder="e.g. Websites, Backends, Clients…" />
						<Button variant="outline" onclick={saveCategory} disabled={savingCategory}>Save</Button>
					</Card.Content>
				</Card.Root>

				{#if d.nativeCron}
					<Card.Root>
						<Card.Header>
							<Card.Title class="text-base">app.json cron (dokku-native)</Card.Title>
						</Card.Header>
						<Card.Content>
							<pre class="text-muted-foreground overflow-x-auto text-xs">{d.nativeCron}</pre>
						</Card.Content>
					</Card.Root>
				{/if}
			</Tabs.Content>

			<Tabs.Content value="env" class="mt-4">
				<Card.Root>
					<Card.Header>
						<Card.Title class="text-base">Environment variables</Card.Title>
						<Card.Description>Saved with <code>--no-restart</code>; toggle below to restart after saving.</Card.Description>
					</Card.Header>
					<Card.Content class="grid gap-2">
						{#each rows as row, i (i)}
							<div class="flex gap-2">
								<Input class="w-56 font-mono text-xs" placeholder="KEY" bind:value={row.key} />
								<Input class="flex-1 font-mono text-xs" placeholder="value" bind:value={row.value} />
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
						<div class="mt-2 flex items-center gap-4">
							<Button variant="outline" size="sm" onclick={() => rows.push({ key: '', value: '' })}>
								<PlusIcon class="size-4" /> Add variable
							</Button>
							<div class="ml-auto flex items-center gap-2">
								<Switch id="restart" bind:checked={restartAfterSave} />
								<Label for="restart" class="text-sm">Restart app</Label>
							</div>
							<Button size="sm" onclick={saveEnv} disabled={savingEnv}>
								{savingEnv ? 'Saving…' : 'Save changes'}
							</Button>
						</div>
					</Card.Content>
				</Card.Root>
			</Tabs.Content>

			<Tabs.Content value="cron" class="mt-4">
				<Card.Root>
					<Card.Header>
						<Card.Title class="text-base">Scheduled jobs</Card.Title>
						<Card.Description>
							Each job runs in a fresh one-off container of this app (<code>dokku --rm run {d.name} …</code>) —
							0MB between runs. Editable live, no redeploy.
						</Card.Description>
					</Card.Header>
					<Card.Content class="grid gap-2">
						{#each jobs as job, i (job.id || i)}
							{@const badge = lastBadge(job.last)}
							<div class="flex items-center gap-2">
								<Input class="w-36 font-mono text-xs" placeholder="0 3 * * *" bind:value={job.schedule} />
								<Input class="flex-1 font-mono text-xs" placeholder="node scripts/cleanup.js" bind:value={job.command} />
								{#if badge}
									<Badge variant={badge.ok ? 'secondary' : 'destructive'} class="whitespace-nowrap">
										{badge.label}
									</Badge>
								{/if}
								<Button
									variant="ghost"
									size="icon"
									onclick={() => (jobs = jobs.filter((_, j) => j !== i))}
									aria-label="Remove job"
								>
									<Trash2Icon class="size-4" />
								</Button>
							</div>
						{:else}
							<p class="text-muted-foreground text-sm">No jobs yet. Schedules use standard cron syntax or @daily / @hourly.</p>
						{/each}
						<div class="mt-2 flex items-center gap-2">
							<Button
								variant="outline"
								size="sm"
								onclick={() => jobs.push({ id: '', schedule: '', command: '' })}
							>
								<PlusIcon class="size-4" /> Add job
							</Button>
							<Button size="sm" class="ml-auto" onclick={saveCron} disabled={savingCron}>
								{savingCron ? 'Saving…' : 'Save jobs'}
							</Button>
						</div>
					</Card.Content>
				</Card.Root>
			</Tabs.Content>

			<Tabs.Content value="logs" class="mt-4">
				<div
					bind:this={logEl}
					class="bg-card h-[32rem] overflow-y-auto rounded-lg border p-4 font-mono text-xs leading-5"
				>
					{#each logLines as line, i (i)}
						<div class="whitespace-pre-wrap">{line}</div>
					{:else}
						<p class="text-muted-foreground">Waiting for logs…</p>
					{/each}
				</div>
			</Tabs.Content>
		</Tabs.Root>
	{/if}
</div>

<Dialog.Root bind:open={deployOpen}>
	<Dialog.Content class="max-w-2xl">
		<Dialog.Header>
			<Dialog.Title>Deploy {name}</Dialog.Title>
			<Dialog.Description>
				Leave the image empty to rebuild from the last deployed source, or give a registry image
				(e.g. <code>ghcr.io/you/app:latest</code>) for <code>git:from-image</code>.
			</Dialog.Description>
		</Dialog.Header>
		<div class="flex gap-2">
			<Input class="flex-1 font-mono text-xs" placeholder="ghcr.io/you/app:latest (optional)" bind:value={image} disabled={deploying} />
			<Button onclick={deploy} disabled={deploying}>
				<RocketIcon class="size-4" />
				{deploying ? 'Deploying…' : 'Deploy'}
			</Button>
		</div>
		{#if deployLines.length}
			<div class="bg-card mt-2 h-64 overflow-y-auto rounded-md border p-3 font-mono text-xs leading-5">
				{#each deployLines as line, i (i)}
					<div class="whitespace-pre-wrap">{line}</div>
				{/each}
			</div>
		{/if}
	</Dialog.Content>
</Dialog.Root>
