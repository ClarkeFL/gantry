<script lang="ts">
	import * as Card from '$lib/components/ui/card';
</script>

<div class="mx-auto max-w-4xl">
	<h1 class="mb-6 text-2xl font-semibold tracking-tight">Backups</h1>
	<Card.Root>
		<Card.Header>
			<Card.Title class="text-base">S3 backups are coming in v2</Card.Title>
			<Card.Description>
				Until then the cron editor already covers it: add a job to any app like
				<code>pg_dump $DATABASE_URL | aws s3 cp - s3://bucket/backup-$(date +%F).sql</code> on a
				<code>@daily</code> schedule. Dokku's database plugins also support
				<code>dokku postgres:backup-schedule &lt;service&gt; &lt;cron&gt; &lt;bucket&gt;</code> natively.
			</Card.Description>
		</Card.Header>
	</Card.Root>
</div>
