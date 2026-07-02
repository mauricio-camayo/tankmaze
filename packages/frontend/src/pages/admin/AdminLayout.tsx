import { Outlet, Link, useLocation } from 'react-router-dom';
import Layout from '../../components/Layout';

const TABS = [
  { to: '/admin/users', label: 'Users' },
  { to: '/admin/tanks', label: 'Tanks' },
  { to: '/admin/ads', label: 'Ads' },
];

export default function AdminLayout() {
  const location = useLocation();

  return (
    <Layout>
      <div style={{ display: 'flex', gap: 4, marginBottom: 24, borderBottom: '1px solid #2d2d4e' }}>
        {TABS.map((tab) => {
          const active = location.pathname.startsWith(tab.to);
          return (
            <Link
              key={tab.to}
              to={tab.to}
              style={{
                padding: '10px 16px', fontSize: 14, textDecoration: 'none',
                color: active ? '#e2e8f0' : '#94a3b8', fontWeight: active ? 600 : 400,
                borderBottom: active ? '2px solid #7c6af7' : '2px solid transparent',
                marginBottom: -1, minHeight: 44, display: 'flex', alignItems: 'center',
              }}
            >
              {tab.label}
            </Link>
          );
        })}
      </div>
      <Outlet />
    </Layout>
  );
}
