import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const apiMock = vi.hoisted(() => ({
  auth: { agentSignupPreview: vi.fn(), agentSignupConfirm: vi.fn(), agentSignupDecline: vi.fn() },
}));
const authMock = vi.hoisted(() => ({
  user: null as { email: string } | null,
  loading: false,
  error: null as unknown,
  retry: vi.fn(),
  refresh: vi.fn(),
  endSession: vi.fn(),
  logout: vi.fn(),
}));
vi.mock('@/lib/api', async () => ({ ...(await vi.importActual<typeof import('@/lib/api')>('@/lib/api')), api: apiMock }));
vi.mock('@/components/auth-provider', () => ({ useAuth: () => authMock }));
vi.mock('@/components/prefs-provider', () => ({ usePrefs: () => ({ timezone: 'UTC' }) }));

import { AgentAccessView } from '@/components/agent-access-view';
import { ApiError } from '@/lib/api';
import type { AgentAccessPreview } from '@/lib/agent-access';
import { forgetHashToken } from '@/lib/hash-token';

const SCOPES = ['analytics:read', 'media:write', 'posts:read', 'posts:schedule', 'posts:write', 'social:connect', 'social:read'];
const preview = (over: Partial<AgentAccessPreview> = {}): AgentAccessPreview => ({
  agentName: 'Claude Desktop', email: 'owner@example.com', scopes: SCOPES, dangerousPolicy: 'trusted_connect', accountExists: false,
  expiresAt: '2026-10-16T10:00:00Z', ...over,
});
const me = { id: 'u1', email: 'owner@example.com' };
const badLink = () => new ApiError(400, 'VALIDATION_ERROR', 'link is invalid or has expired');

function openLink(token = 'tok_123') {
  window.history.replaceState(null, '', `/agent-access#token=${token}`);
  render(<AgentAccessView />);
}

beforeEach(() => {
  forgetHashToken();
  window.history.replaceState(null, '', '/');
  Object.values(apiMock.auth).forEach((f) => f.mockReset());
  apiMock.auth.agentSignupPreview.mockResolvedValue(preview());
  apiMock.auth.agentSignupConfirm.mockResolvedValue({ me, accountCreated: true });
  apiMock.auth.agentSignupDecline.mockResolvedValue(undefined);
  Object.assign(authMock, { user: null, loading: false, error: null });
  [authMock.retry, authMock.refresh, authMock.endSession, authMock.logout].forEach((f) => f.mockReset());
  authMock.refresh.mockResolvedValue(undefined);
  authMock.logout.mockResolvedValue(undefined);
});

describe('/agent-access: loading and broken links', () => {
  it('shows a status while checking, and takes the token out of the address bar', () => {
    apiMock.auth.agentSignupPreview.mockReturnValue(new Promise(() => undefined));
    openLink('abc_DEF-1');
    expect(screen.getByRole('status')).toHaveTextContent('Checking the link');
    expect(apiMock.auth.agentSignupPreview).toHaveBeenCalledWith('abc_DEF-1');
    expect(window.location.href).not.toContain('abc_DEF-1');
  });

  it('asks to open the mailed link again when there is no token', async () => {
    window.history.replaceState(null, '', '/agent-access');
    render(<AgentAccessView />);
    expect(await screen.findByRole('heading', { name: 'Open the link from the email' })).toBeInTheDocument();
    expect(apiMock.auth.agentSignupPreview).not.toHaveBeenCalled();
  });

  it('says the link no longer works on a 400', async () => {
    apiMock.auth.agentSignupPreview.mockRejectedValue(badLink());
    openLink();
    expect(await screen.findByRole('heading', { name: 'This link no longer works' })).toBeInTheDocument();
    expect(screen.getByText(/Links last 24 hours/)).toBeInTheDocument();
  });

  it('offers a retry when the check failed for another reason', async () => {
    apiMock.auth.agentSignupPreview.mockRejectedValueOnce(new ApiError(503, 'UNAVAILABLE', 'down')).mockResolvedValueOnce(preview());
    openLink();
    expect(await screen.findByRole('heading', { name: 'Could not check the link' })).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: /try again|retry/i }));
    expect(await screen.findByRole('button', { name: 'Create account and confirm' })).toBeInTheDocument();
  });
});

describe('/agent-access: a new account', () => {
  it('names the agent and the address, and lists only what the key can and cannot do', async () => {
    openLink();
    expect(await screen.findByText('Claude Desktop wants to create a Steerpost account for owner@example.com and work in it for you.')).toBeInTheDocument();
    expect(screen.getByText(/Schedule posts at least 5 minutes ahead/)).toBeInTheDocument();
    expect(screen.getByText(/Connect networks with credentials you give it/)).toBeInTheDocument();
    expect(screen.getByText('Publish immediately.')).toBeInTheDocument();
    expect(screen.getByText('Delete posts.')).toBeInTheDocument();
    expect(screen.getByText('Disconnect accounts.')).toBeInTheDocument();
  });

  it('does not list connecting when the key cannot connect', async () => {
    apiMock.auth.agentSignupPreview.mockResolvedValue(preview({ scopes: ['posts:read'] }));
    openLink();
    expect(await screen.findByText('See your connected accounts and posts.')).toBeInTheDocument();
    expect(screen.queryByText(/Connect networks/)).not.toBeInTheDocument();
  });

  it('needs the Terms before it confirms (D-016), and says why', async () => {
    openLink();
    await userEvent.click(await screen.findByRole('button', { name: 'Create account and confirm' }));
    expect(screen.getByText('Accept the Terms and the Privacy Policy to create an account.')).toBeInTheDocument();
    expect(apiMock.auth.agentSignupConfirm).not.toHaveBeenCalled();
  });

  it('creates the account with the name, then asks to set a password', async () => {
    openLink('tok_new');
    await userEvent.type(await screen.findByLabelText('Name (optional)'), ' Sam ');
    await userEvent.click(screen.getByRole('checkbox'));
    await userEvent.click(screen.getByRole('button', { name: 'Create account and confirm' }));
    await waitFor(() => expect(apiMock.auth.agentSignupConfirm).toHaveBeenCalledWith({ token: 'tok_new', accept_terms: true, display_name: 'Sam' }));
    expect(await screen.findByRole('heading', { name: 'Access confirmed' })).toBeInTheDocument();
    expect(screen.getByText(/no password yet/)).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Set a password' })).toHaveAttribute('href', '/settings');
    expect(authMock.refresh).toHaveBeenCalled();
  });

  it('keeps the form when the server refuses the Terms', async () => {
    apiMock.auth.agentSignupConfirm.mockRejectedValue(Object.assign(badLink(), { fields: { accept_terms: 'must be accepted' } }));
    openLink();
    await userEvent.click(await screen.findByRole('checkbox'));
    await userEvent.click(screen.getByRole('button', { name: 'Create account and confirm' }));
    expect(await screen.findByText('Accept the Terms and the Privacy Policy to create an account.')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Create account and confirm' })).toBeInTheDocument();
  });

  it('shows the broken-link screen when the link died before confirming', async () => {
    apiMock.auth.agentSignupConfirm.mockRejectedValue(badLink());
    openLink();
    await userEvent.click(await screen.findByRole('checkbox'));
    await userEvent.click(screen.getByRole('button', { name: 'Create account and confirm' }));
    expect(await screen.findByRole('heading', { name: 'This link no longer works' })).toBeInTheDocument();
  });

  it('refuses with "Not me" and says the key never works', async () => {
    openLink('tok_no');
    await userEvent.click(await screen.findByRole('button', { name: 'Not me, refuse' }));
    expect(apiMock.auth.agentSignupDecline).toHaveBeenCalledWith('tok_no');
    expect(await screen.findByRole('heading', { name: 'Request refused' })).toBeInTheDocument();
  });

  it('shows why a refusal failed and keeps the page', async () => {
    apiMock.auth.agentSignupDecline.mockRejectedValue(new ApiError(503, 'UNAVAILABLE', 'down'));
    openLink();
    await userEvent.click(await screen.findByRole('button', { name: 'Not me, refuse' }));
    expect(await screen.findByRole('alert')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Create account and confirm' })).toBeInTheDocument();
  });
});

describe('/agent-access: an existing account', () => {
  beforeEach(() => {
    apiMock.auth.agentSignupPreview.mockResolvedValue(preview({ accountExists: true }));
    apiMock.auth.agentSignupConfirm.mockResolvedValue({ me, accountCreated: false });
  });

  it('asks a signed-out visitor to sign in and come back, and still lets them refuse', async () => {
    openLink();
    expect(await screen.findByText(/Sign in as owner@example.com to confirm it/)).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Sign in to confirm' })).toHaveAttribute('href', '/login?next=%2Fagent-access');
    expect(screen.getByRole('button', { name: 'Not me, refuse' })).toBeInTheDocument();
    expect(screen.queryByRole('checkbox')).not.toBeInTheDocument();
  });

  it('lets the owner confirm without the Terms box, then points to Developer', async () => {
    authMock.user = { email: 'Owner@Example.com' };
    openLink('tok_mine');
    await userEvent.click(await screen.findByRole('button', { name: 'Confirm access' }));
    expect(screen.queryByRole('checkbox')).not.toBeInTheDocument();
    await waitFor(() => expect(apiMock.auth.agentSignupConfirm).toHaveBeenCalledWith({ token: 'tok_mine', accept_terms: false }));
    expect(await screen.findByRole('link', { name: 'Go to Developer' })).toHaveAttribute('href', '/developer');
    expect(authMock.refresh).not.toHaveBeenCalled();
  });

  it('falls back to the sign-in prompt when the session ended meanwhile (401)', async () => {
    authMock.user = { email: 'owner@example.com' };
    apiMock.auth.agentSignupConfirm.mockRejectedValue(new ApiError(401, 'UNAUTHENTICATED', 'Authentication required'));
    openLink();
    await userEvent.click(await screen.findByRole('button', { name: 'Confirm access' }));
    await waitFor(() => expect(authMock.endSession).toHaveBeenCalled());
  });

  it('asks someone signed in as another account to sign out first', async () => {
    authMock.user = { email: 'someone@example.com' };
    openLink();
    expect(await screen.findByRole('heading', { name: 'You are signed in as someone else' })).toBeInTheDocument();
    expect(screen.getByText(/You are signed in as someone@example.com. This request is for owner@example.com./)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Confirm access' })).not.toBeInTheDocument();
    await act(async () => userEvent.click(screen.getByRole('button', { name: 'Sign out' })));
    expect(authMock.logout).toHaveBeenCalled();
  });
});

describe('/agent-access: a new account while someone is signed in', () => {
  it('asks to sign out instead of offering to create the account', async () => {
    authMock.user = { email: 'someone@example.com' };
    openLink();
    expect(await screen.findByRole('heading', { name: 'You are signed in as someone else' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Create account and confirm' })).not.toBeInTheDocument();
  });
});
