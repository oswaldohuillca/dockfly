import { createFileRoute } from '@tanstack/react-router';

import { ChangePassword } from '@/components/change-password';
import { TwoFactor } from '@/components/two-factor';

export const Route = createFileRoute('/_app/account')({
  component: Account,
});

function Account() {
  return (
    <div className="space-y-4">
      <ChangePassword />
      <TwoFactor />
    </div>
  );
}
