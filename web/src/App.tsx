import { useEffect, useState } from "react";
import { Routes, Route, Navigate, NavLink, useNavigate, useLocation } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { get } from "./api/client";
import type { Me } from "./api/types";
import { syncPushSubscription } from "./push";
import {
  HomeIcon,
  ActivityIcon,
  TagIcon,
  RepeatIcon,
  SettingsIcon,
  MenuIcon,
  SlidersIcon,
  UsersIcon,
  ChevronRightIcon,
  ChartIcon,
} from "./components/Icons";
import Login from "./pages/Login";
import Onboarding from "./pages/Onboarding";
import Dashboard from "./pages/Dashboard";
import Transactions from "./pages/Transactions";
import TransactionDetail from "./pages/TransactionDetail";
import Categories from "./pages/Categories";
import Rules from "./pages/Rules";
import Recurring from "./pages/Recurring";
import CashFlow from "./pages/CashFlow";
import Family from "./pages/Family";
import Settings from "./pages/Settings";

/** Everything that doesn't earn a permanent tab slot. */
const MENU_ITEMS = [
  { to: "/cashflow", Icon: ChartIcon, label: "Cash flow" },
  { to: "/categories", Icon: TagIcon, label: "Categories" },
  { to: "/rules", Icon: SlidersIcon, label: "Rules" },
  { to: "/family", Icon: UsersIcon, label: "Family" },
  { to: "/settings", Icon: SettingsIcon, label: "Settings" },
];

function TabBar() {
  const [menuOpen, setMenuOpen] = useState(false);
  const location = useLocation();

  // Close the sheet whenever navigation lands somewhere new.
  useEffect(() => setMenuOpen(false), [location.pathname]);

  const tab = (to: string, Icon: (p: { size?: number }) => JSX.Element, label: string) => (
    <NavLink to={to} className={({ isActive }) => (isActive ? "active" : "")} end={to === "/"}>
      <span className="icon">
        <Icon size={24} />
      </span>
      {label}
    </NavLink>
  );
  const menuActive = MENU_ITEMS.some((m) => location.pathname.startsWith(m.to));

  return (
    <>
      {menuOpen && (
        <>
          <div className="sheet-scrim" onClick={() => setMenuOpen(false)} />
          <div className="sheet" role="dialog" aria-label="More">
            {MENU_ITEMS.map(({ to, Icon, label }) => (
              <NavLink key={to} to={to} className="sheet-item">
                <Icon size={20} />
                <span className="grow">{label}</span>
                <ChevronRightIcon size={16} />
              </NavLink>
            ))}
          </div>
        </>
      )}
      <nav className="tabbar">
        {tab("/", HomeIcon, "Home")}
        {tab("/transactions", ActivityIcon, "Activity")}
        {tab("/recurring", RepeatIcon, "Recurring")}
        <button
          className={menuActive || menuOpen ? "active" : ""}
          onClick={() => setMenuOpen(!menuOpen)}
          aria-expanded={menuOpen}
        >
          <span className="icon">
            <MenuIcon size={24} />
          </span>
          More
        </button>
      </nav>
    </>
  );
}

export default function App() {
  const navigate = useNavigate();
  const location = useLocation();

  const me = useQuery<Me>({
    queryKey: ["me"],
    queryFn: () => get<Me>("/api/me"),
    retry: false,
  });

  useEffect(() => {
    const onUnauth = () => navigate("/login", { replace: true });
    window.addEventListener("crewmate:unauthenticated", onUnauth);
    return () => window.removeEventListener("crewmate:unauthenticated", onUnauth);
  }, [navigate]);

  // Re-register the push subscription on every authenticated launch — iOS
  // silently drops subscriptions.
  useEffect(() => {
    if (me.data) syncPushSubscription();
  }, [me.data]);

  if (location.pathname === "/login") {
    return (
      <main className="app-main">
        <Login />
      </main>
    );
  }

  if (me.isLoading) {
    return (
      <main className="app-main">
        <div className="center">
          <div className="spinner" />
        </div>
      </main>
    );
  }
  if (me.isError) {
    return <Navigate to="/login" replace />;
  }

  const hasFamily = !!me.data?.family_id;
  if (!hasFamily && location.pathname !== "/onboarding") {
    return <Navigate to="/onboarding" replace />;
  }

  return (
    <>
      <main className="app-main">
        {me.data?.crew_status === "needs_relogin" && location.pathname !== "/login" && (
          <div className="banner">
            Crewmate lost access to your Crew account.{" "}
            <a href="/login">Sign in again</a> to keep tracking transactions.
          </div>
        )}
        <Routes>
          <Route path="/" element={<Dashboard />} />
          <Route path="/onboarding" element={<Onboarding />} />
          <Route path="/transactions" element={<Transactions />} />
          <Route path="/transactions/:id" element={<TransactionDetail />} />
          <Route path="/categories" element={<Categories />} />
          <Route path="/rules" element={<Rules />} />
          <Route path="/recurring" element={<Recurring />} />
          <Route path="/cashflow" element={<CashFlow />} />
          <Route path="/family" element={<Family />} />
          <Route path="/settings" element={<Settings />} />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </main>
      {hasFamily && <TabBar />}
    </>
  );
}
