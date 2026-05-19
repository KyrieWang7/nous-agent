/** 旧路由：重定向到独立 /drama/projects/[id] */
import { redirect } from "next/navigation";

export default async function DramaProjectDetailWorkspacePage({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = await params;
  redirect(`/drama/projects/${id}`);
}
