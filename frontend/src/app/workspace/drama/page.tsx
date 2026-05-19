/** 旧路由：重定向到独立 /drama 入口 */
import { redirect } from "next/navigation";

export default function DramaWorkspacePage() {
  redirect("/drama/projects");
}
