import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { enableSkill, loadSkillDetail } from "./api";

import { loadSkills } from ".";

export function useSkills() {
  const { data, isLoading, error } = useQuery({
    queryKey: ["skills"],
    queryFn: () => loadSkills(),
  });
  return { skills: data ?? [], isLoading, error };
}

export function useSkillDetail(skillName: string | null) {
  const { data, isLoading, error } = useQuery({
    queryKey: ["skill-detail", skillName],
    queryFn: () => loadSkillDetail(skillName!),
    enabled: !!skillName,
  });
  return { skill: data ?? null, isLoading, error };
}

export function useEnableSkill() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async ({
      skillName,
      enabled,
    }: {
      skillName: string;
      enabled: boolean;
    }) => {
      await enableSkill(skillName, enabled);
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["skills"] });
    },
  });
}
