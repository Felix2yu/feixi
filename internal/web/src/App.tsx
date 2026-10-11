import { useQuery } from "@tanstack/react-query";
import { NavLink, Outlet } from "react-router-dom";
import { api } from "./lib/api";
import { cx } from "./components/ui";

const links = [
  { to: "/", label: "仪表板", end: true },
  { to: "/channels", label: "通知渠道" },
  { to: "/badges", label: "徽章" },
  { to: "/history", label: "通知历史" },
  { to: "/settings", label: "设置" },
];

export default function App() {
  const project = useQuery({ queryKey: ["project"], queryFn: api.project });

  return (
    <div className="min-h-full">
      <header className="sticky top-0 z-40 border-b border-line bg-surface/90 backdrop-blur dark:bg-canvas/90">
        <div className="mx-auto flex max-w-6xl items-center gap-x-4 px-4 py-2.5">
          <div className="flex min-w-0 items-center gap-4">
            <span className="shrink-0 text-sm font-semibold">飞檄</span>
            <nav className="flex min-w-0 items-center gap-1 overflow-x-auto whitespace-nowrap">
              {links.map((l) => (
                <NavLink
                  key={l.to}
                  to={l.to}
                  end={l.end}
                  className={({ isActive }) =>
                    cx(
                      "rounded-md px-2 py-1 text-sm",
                      isActive ? "bg-ink text-white" : "text-ink-soft hover:text-ink",
                    )
                  }
                >
                  {l.label}
                </NavLink>
              ))}
            </nav>
          </div>
          <div className="ml-auto hidden shrink-0 text-xs text-ink-soft sm:block">
            {project.data ? `${project.data.name} · ${project.data.check_count}/${project.data.max_checks} 检查项` : "…"}
          </div>
        </div>
      </header>
      <main className="mx-auto max-w-6xl px-4 py-5">
        <Outlet />
      </main>
    </div>
  );
}
