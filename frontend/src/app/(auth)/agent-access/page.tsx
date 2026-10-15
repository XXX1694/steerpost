import { AgentAccessView } from '@/components/agent-access-view';
import { AgentAccessScope } from '@/i18n/scopes/agent-access';

export const metadata = { title: 'Agent access' };

export default function Page() {
  return (
    <AgentAccessScope>
      <AgentAccessView />
    </AgentAccessScope>
  );
}
