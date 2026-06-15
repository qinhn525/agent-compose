<script lang="ts">
  import { onMount } from 'svelte';

  import { getAuthStatus } from '../api/auth';
  import { appPath } from '../paths';

  let oauthEnabled = false;
  let loading = true;
  let error = '';

  function returnPath(): string {
    const params = new URLSearchParams(window.location.search);
    const next = params.get('next');
    if (next?.startsWith('/') && !next.startsWith('//') && !next.startsWith(appPath('/login'))) {
      return next;
    }
    return appPath('/');
  }

  async function loadStatus(): Promise<void> {
    const status = await getAuthStatus();
    oauthEnabled = Boolean(status.oauthEnabled);
    if (!status.enabled || status.loggedIn) {
      window.location.replace(returnPath());
      return;
    }
  }

  function oauthLogin(): void {
    const next = returnPath();
    window.location.assign(`${appPath('/oauth/authorize')}?next=${encodeURIComponent(next)}`);
  }

  onMount(() => {
    void loadStatus()
      .catch((err) => {
        error = err instanceof Error ? err.message : String(err);
      })
      .finally(() => {
        loading = false;
      });
  });
</script>

<main class="auth-page">
  <section class="auth-panel">
    <div class="auth-copy">
      <p class="auth-eyebrow">agent-compose</p>
      <h1>登录控制台</h1>
    </div>

    {#if loading}
      <p class="muted">正在检查登录状态...</p>
    {:else}
      <div class="auth-actions">
        {#if error}
          <div class="alert danger">{error}</div>
        {/if}
        {#if oauthEnabled}
          <button class="primary auth-button" type="button" on:click={oauthLogin}>Auth 登录</button>
        {:else}
          <button class="primary auth-button" type="button" disabled>Auth 登录未配置</button>
        {/if}
      </div>
    {/if}
  </section>
</main>
