import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiKeysView } from '@/components/developer/api-keys-view';
import type { ApiKey } from '@/lib/types';

const apiMock = vi.hoisted(() => ({
  developer: { apiKeys: vi.fn(), createApiKey: vi.fn(), revokeApiKey: vi.fn() },
}));
vi.mock('@/lib/api', () => ({ api: apiMock, ApiError: class ApiError extends Error {} }));
vi.mock('@/components/prefs-provider', () => ({ usePrefs: () => ({ timezone: 'UTC' }) }));
vi.mock('@/components/toast', () => ({ useToast: () => ({ success: vi.fn(), error: vi.fn() }) }));

const key = (over: Partial<ApiKey>): ApiKey => ({
  id: 'k' + Math.random(), name: 'Key', prefix: 'sk_live_abcd', scopes: ['posts:read'], expires_at: null, revoked_at: null,
  last_used_at: null, created_at: '2026-10-01T00:00:00Z', dangerous_policy: 'approve', ...over,
});

beforeEach(() => {
  apiMock.developer.apiKeys.mockReset().mockResolvedValue([]);
  apiMock.developer.createApiKey.mockReset().mockResolvedValue({ key: key({}), rawKey: 'sk_live_fake' }); // gitleaks:allow (fake test value)
});

async function openCreate() {
  render(<ApiKeysView />);
  await userEvent.click(await screen.findByRole('button', { name: 'Create key' }));
  await userEvent.type(await screen.findByLabelText('Name'), 'CI publisher');
}

const tick = (id: string) => userEvent.click(document.getElementById(id) as HTMLElement);
const tickAck = () => userEvent.click(screen.getByLabelText(/I understand this key can ask to publish/));

describe('key policy in the key list', () => {
  it('shows which keys act without asking and which ask first; a revoked key shows neither', async () => {
    apiMock.developer.apiKeys.mockResolvedValue([
      key({ name: 'Release bot', dangerous_policy: 'trusted' }),
      key({ name: 'CI reader' }),
      key({ name: 'Old', revoked_at: '2026-10-02T00:00:00Z', dangerous_policy: 'trusted' }),
    ]);
    render(<ApiKeysView />);
    expect(await screen.findAllByText('Trusted: acts without asking')).toHaveLength(1);
    expect(screen.getAllByText('Asks before dangerous actions')).toHaveLength(1);
  });

  it('marks a key an agent signed up for as one that can connect accounts, not as trusted (D-025)', async () => {
    apiMock.developer.apiKeys.mockResolvedValue([key({ name: 'Claude Desktop', scopes: ['social:connect'], dangerous_policy: 'trusted_connect' })]);
    render(<ApiKeysView />);
    expect(await screen.findByText('Can connect accounts')).toBeInTheDocument();
    expect(screen.queryByText('Trusted: acts without asking')).not.toBeInTheDocument();
    expect(screen.queryByText('Asks before dangerous actions')).not.toBeInTheDocument();
  });
});

describe('creating a trusted key', () => {
  it('offers the trusted option only when a dangerous scope is selected, off by default', async () => {
    await openCreate();
    expect(screen.queryByLabelText(/Trusted key/)).not.toBeInTheDocument();
    await tick('scope-posts:publish');
    expect(screen.getByLabelText(/Trusted key/)).not.toBeChecked();
  });

  it('sends policy approve by default, even with dangerous scopes', async () => {
    await openCreate();
    await tick('scope-posts:publish');
    await tickAck();
    await userEvent.click(screen.getByRole('button', { name: 'Create key' }));
    await waitFor(() => expect(apiMock.developer.createApiKey).toHaveBeenCalled());
    expect(apiMock.developer.createApiKey.mock.calls[0]![0]).toMatchObject({ dangerous_policy: 'approve' });
  });

  it('needs a separate confirmation before a trusted key can be created', async () => {
    await openCreate();
    await tick('scope-posts:publish');
    await tickAck();
    await tick('key-trusted');
    expect(screen.getByText(/A trusted key acts without asking/)).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: 'Create key' }));
    expect(await screen.findByText(/Confirm that this key may act without your approval/)).toBeInTheDocument();
    expect(apiMock.developer.createApiKey).not.toHaveBeenCalled();

    await tick('key-trusted-ack');
    await userEvent.click(screen.getByRole('button', { name: 'Create key' }));
    await waitFor(() => expect(apiMock.developer.createApiKey).toHaveBeenCalled());
    expect(apiMock.developer.createApiKey.mock.calls[0]![0]).toMatchObject({ dangerous_policy: 'trusted', scopes: expect.arrayContaining(['posts:publish']) });
  });

  it('turning trusted off again clears the confirmation, and removing the dangerous scopes drops the option', async () => {
    await openCreate();
    await tick('scope-posts:publish');
    await tickAck();
    await tick('key-trusted');
    await tick('key-trusted-ack');
    await tick('key-trusted');
    await tick('key-trusted');
    expect(screen.getByLabelText(/I understand this key can publish, delete and disconnect without/)).not.toBeChecked();
    await tick('key-trusted-ack');
    await tick('scope-posts:publish'); // untick the only dangerous scope
    await userEvent.click(screen.getByRole('button', { name: 'Create key' }));
    await waitFor(() => expect(apiMock.developer.createApiKey).toHaveBeenCalled());
    expect(apiMock.developer.createApiKey.mock.calls[0]![0]).toMatchObject({ dangerous_policy: 'approve' });
    fireEvent.keyDown(document.body, { key: 'Escape' });
  });
});
