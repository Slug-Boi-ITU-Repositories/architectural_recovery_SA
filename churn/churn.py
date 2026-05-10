# Most of this code is taken from the provided code from Mircea Lungu in our Software Architecture class
# https://architecture-recovery.github.io/3_EvolutionaryAnalysis

from pydriller import Repository
from git import Repo
import os
import re

CODE_ROOT_FOLDER = "golangci-lint"

if not os.path.exists(CODE_ROOT_FOLDER):
  Repo.clone_from("https://github.com/golangci/golangci-lint", CODE_ROOT_FOLDER)

  def file_path(file_name):
    return CODE_ROOT_FOLDER+file_name

REPO_DIR = 'golangci-lint'

all_commits = list(Repository(REPO_DIR).traverse_commits())

def print_out_commit_details(commits):
    for commit in commits:
        print(commit)
        for each in commit.modified_files:
            print(f"{commit.author.name} {each.change_type} {each.filename}\n -{each.old_path}\n -{each.new_path}")

# print_out_commit_details(all_commits)


from collections import defaultdict

commit_counts = defaultdict(int)
commit_counts_non_none = defaultdict(int)

gofiles = re.compile(r'.+\.go$')

for commit in all_commits:
    for each in commit.modified_files:
        if each.filename.endswith(".go"):
            try:
                commit_counts [each.new_path] += 1
                commit_counts_non_none[each.filename] += 1
            except:
                pass

# sort by number of commits in decreasing order
print(len(commit_counts_non_none))
# modified_sorted = sorted(commit_counts.items(), key=lambda x: x[1], reverse=True)[:100]
modified_sorted_non_none = sorted(commit_counts_non_none.items(), key=lambda x: x[1], reverse=True)

# print("##Changes to files (Deletions counted as None)##")
# print("Filename -> commits:")
# for c in modified_sorted:
#    # None being removed/deleted files
#    print(c)

print("##Changes to files (Pure Not looking at deletions)##")
print("Filename -> commits:")
for c in modified_sorted_non_none:
    print(c)

import csv

# Write data to a csv file for use in gotool
headers = ['filename', 'commit_count']

with open('churn.csv', 'w', newline='', encoding='utf-8') as f:
    writer = csv.writer(f)
    writer.writerow(headers)
    writer.writerows(modified_sorted_non_none)
