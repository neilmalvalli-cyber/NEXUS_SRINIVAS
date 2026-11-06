import Link from "next/link";
import { EmptyState } from "@/components/nexus";

export default function NotFound() {
  return (
    <EmptyState title="Page not found">
      <Link href="/" className="underline underline-offset-4 hover:text-foreground">
        Back to the overview
      </Link>
    </EmptyState>
  );
}
