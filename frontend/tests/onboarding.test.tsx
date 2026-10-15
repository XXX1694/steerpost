import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const apiMock = vi.hoisted(() => ({
  posts: { list: vi.fn() },
  developer: { apiKeys: vi.fn(), mcpConnections: vi.fn() },
  approvals: { list: vi.fn() },
}));
vi.mock('@/lib/api', async () => ({ ...(await vi.importActual<typeof import('@/lib/api')>('@/lib/api')), api: apiMock }));

import { OnboardingChecklist } from '@/components/onboarding-checklist';
import { ONBOARDING_COMPLETE_KEY, ONBOARDING_DISMISSED_KEY, onboardingSteps, requiredDone } from '@/lib/onboarding';

const empty = { items: [], next_cursor: null };
const live = { revoked_at: null, expires_at: null };

beforeEach(() => {
  window.localStorage.clear();
  apiMock.posts.list.mockResolvedValue(empty);
  apiMock.developer.apiKeys.mockResolvedValue([]);
  apiMock.developer.mcpConnections.mockResolvedValue([]);
  apiMock.approvals.list.mockResolvedValue(empty);
});

describe('onboardingSteps', () => {
  const base = { connectedAccounts: 0, hasPost: false, apiKeys: [], mcpConnections: [], hasApproval: false };
  it('is all open for a new user', () => {
    const steps = onboardingSteps(base);
    expect(steps.map((s) => s.done)).toEqual([false, false, false, false]);
    expect(requiredDone(steps)).toBe(false);
  });
  it('ignores revoked keys and connections', () => {
    const steps = onboardingSteps({ ...base, apiKeys: [{ revoked_at: 'x', expires_at: null }], mcpConnections: [{ revoked_at: 'y' }] });
    expect(steps[2]?.done).toBe(false);
  });
  it('ignores an expired key', () => {
    const steps = onboardingSteps({ ...base, apiKeys: [{ revoked_at: null, expires_at: '2020-01-01T00:00:00Z' }] }, new Date('2026-01-01'));
    expect(steps[2]?.done).toBe(false);
  });
  it('counts a live key or a live connection as an agent', () => {
    expect(onboardingSteps({ ...base, apiKeys: [live] })[2]?.done).toBe(true);
    expect(onboardingSteps({ ...base, mcpConnections: [live] })[2]?.done).toBe(true);
  });
  it('does not need the optional approval step to be complete', () => {
    const steps = onboardingSteps({ ...base, connectedAccounts: 1, hasPost: true, apiKeys: [live] });
    expect(requiredDone(steps)).toBe(true);
    expect(steps[3]?.done).toBe(false);
  });
});

describe('OnboardingChecklist', () => {
  it('shows every step with its action link for a new user', async () => {
    render(<OnboardingChecklist connectedAccounts={0} />);
    expect(await screen.findByRole('heading', { name: 'Get started' })).toBeInTheDocument();
    expect(screen.getByText('0 of 4 steps done.')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Connect account' })).toHaveAttribute('href', '/accounts');
    expect(screen.getByRole('link', { name: 'Write a post' })).toHaveAttribute('href', '/compose');
    expect(screen.getByRole('link', { name: 'Connect agent' })).toHaveAttribute('href', '/developer/mcp');
    expect(screen.getByRole('link', { name: 'Open approvals' })).toHaveAttribute('href', '/approvals');
  });

  it('ticks steps from real data and drops their action', async () => {
    apiMock.posts.list.mockResolvedValue({ items: [{ id: 'p1' }], next_cursor: null });
    render(<OnboardingChecklist connectedAccounts={2} />);
    expect(await screen.findByText('2 of 4 steps done.')).toBeInTheDocument();
    expect(screen.queryByRole('link', { name: 'Connect account' })).not.toBeInTheDocument();
    expect(screen.queryByRole('link', { name: 'Write a post' })).not.toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Connect agent' })).toBeInTheDocument();
  });

  it('says the user is set up when the required steps are done', async () => {
    apiMock.posts.list.mockResolvedValue({ items: [{ id: 'p1' }], next_cursor: null });
    apiMock.developer.mcpConnections.mockResolvedValue([live]);
    render(<OnboardingChecklist connectedAccounts={1} />);
    expect(await screen.findByText('Set up')).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'You are set up' })).not.toBeInTheDocument(); // collapsed to one line
    await userEvent.click(screen.getByRole('button', { name: 'Show steps' }));
    expect(screen.getByRole('heading', { name: 'You are set up' })).toBeInTheDocument();
  });

  it('keeps a next step and logs the cause when the data cannot be read and there is no account', async () => {
    const spy = vi.spyOn(console, 'error').mockImplementation(() => {});
    apiMock.developer.apiKeys.mockRejectedValue(new Error('down'));
    render(<OnboardingChecklist connectedAccounts={0} />);
    expect(await screen.findByRole('link', { name: 'Connect account' })).toHaveAttribute('href', '/accounts');
    expect(screen.queryByRole('heading', { name: 'Get started' })).not.toBeInTheDocument();
    // The "Connect account" link renders before the failed request settles; wait for the log rather than race it.
    await waitFor(() => expect(spy).toHaveBeenCalled());
    spy.mockRestore();
  });

  it('stays hidden, and logs, when the data cannot be read but an account exists', async () => {
    const spy = vi.spyOn(console, 'error').mockImplementation(() => {});
    apiMock.developer.apiKeys.mockRejectedValue(new Error('down'));
    render(<OnboardingChecklist connectedAccounts={1} />);
    await waitFor(() => expect(spy).toHaveBeenCalled());
    expect(screen.queryByRole('heading', { name: 'Get started' })).not.toBeInTheDocument();
    expect(screen.queryByRole('link', { name: 'Connect account' })).not.toBeInTheDocument();
    spy.mockRestore();
  });

  it('remembers completion and stops fetching', async () => {
    apiMock.posts.list.mockResolvedValue({ items: [{ id: 'p1' }], next_cursor: null });
    apiMock.developer.mcpConnections.mockResolvedValue([live]);
    const { unmount } = render(<OnboardingChecklist connectedAccounts={1} />);
    await screen.findByText('Set up');
    await waitFor(() => expect(window.localStorage.getItem(ONBOARDING_COMPLETE_KEY)).toBe('1'));
    unmount();
    apiMock.posts.list.mockClear();
    render(<OnboardingChecklist connectedAccounts={1} />);
    expect(await screen.findByRole('button', { name: 'Show setup checklist' })).toBeInTheDocument();
    expect(apiMock.posts.list).not.toHaveBeenCalled();
  });

  it('shows the checklist again on request', async () => {
    const user = userEvent.setup();
    window.localStorage.setItem(ONBOARDING_DISMISSED_KEY, '1');
    render(<OnboardingChecklist connectedAccounts={1} />);
    await user.click(await screen.findByRole('button', { name: 'Show setup checklist' }));
    expect(await screen.findByRole('heading', { name: 'Get started' })).toBeInTheDocument();
  });

  it('dismisses and remembers it', async () => {
    const user = userEvent.setup();
    const { unmount } = render(<OnboardingChecklist connectedAccounts={1} />);
    await user.click(await screen.findByRole('button', { name: 'Dismiss setup checklist' }));
    expect(screen.queryByRole('heading', { name: 'Get started' })).not.toBeInTheDocument();
    expect(window.localStorage.getItem(ONBOARDING_DISMISSED_KEY)).toBe('1');
    unmount();
    render(<OnboardingChecklist connectedAccounts={1} />);
    const calls = apiMock.posts.list.mock.calls.length;
    await new Promise((r) => setTimeout(r, 20));
    expect(apiMock.posts.list.mock.calls.length).toBe(calls);
    expect(screen.queryByRole('heading', { name: 'Get started' })).not.toBeInTheDocument();
  });

  it('keeps a next step for an account without networks after dismissal', async () => {
    window.localStorage.setItem(ONBOARDING_DISMISSED_KEY, '1');
    render(<OnboardingChecklist connectedAccounts={0} />);
    expect(await screen.findByRole('link', { name: 'Connect account' })).toHaveAttribute('href', '/accounts');
  });
});
