export interface Skill {
  name: string;
  description: string;
  category: string;
  license: string;
  enabled: boolean;
}

export interface SkillDetail extends Skill {
  content: string;
}
