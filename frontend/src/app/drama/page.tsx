"use client";

import { redirect } from "next/navigation";

/**
 * Drama Main Page - redirects to projects list
 */
export default function DramaPage() {
  redirect("/drama/projects");
}
