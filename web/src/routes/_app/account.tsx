import { createFileRoute } from '@tanstack/react-router';

import { PageHeader } from '@/components/page-header';
import { Trans } from '@lingui/react/macro';
import { APITokens } from '@/components/api-tokens';
import { ChangePassword } from '@/components/change-password';
import { TwoFactor } from '@/components/two-factor';

export const Route = createFileRoute('/_app/account')({
  component: Account,
});

function Account() {
  return (
    <div className="space-y-5">
      <PageHeader title={<Trans>My account</Trans>} />
      <ChangePassword />
      <TwoFactor />
      <APITokens />
    </div>
  );
}
