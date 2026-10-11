import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createRoot } from "react-dom/client";
import { createBrowserRouter, RouterProvider } from "react-router-dom";
import App from "./App";
import { ToastProvider } from "./components/ui";
import Badges from "./pages/Badges";
import Channels from "./pages/Channels";
import CheckDetail from "./pages/CheckDetail";
import CheckForm from "./pages/CheckForm";
import Dashboard from "./pages/Dashboard";
import History from "./pages/History";
import Settings from "./pages/Settings";
import StatusPage from "./pages/StatusPage";
import "./index.css";

const queryClient = new QueryClient({
  defaultOptions: {
    queries: { staleTime: 2_000, refetchOnWindowFocus: true, retry: 1 },
  },
});

const router = createBrowserRouter([
  { path: "/status/:key", element: <StatusPage /> },
  {
    path: "/",
    element: <App />,
    children: [
      { index: true, element: <Dashboard /> },
      { path: "checks/new", element: <CheckForm /> },
      { path: "checks/:uuid", element: <CheckDetail /> },
      { path: "checks/:uuid/edit", element: <CheckForm /> },
      { path: "channels", element: <Channels /> },
      { path: "badges", element: <Badges /> },
      { path: "history", element: <History /> },
      { path: "settings", element: <Settings /> },
    ],
  },
]);

createRoot(document.getElementById("root")!).render(
  <ToastProvider>
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  </ToastProvider>,
);
