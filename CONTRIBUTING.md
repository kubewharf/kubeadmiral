# Contributing to KubeAdmiral

## Code of Conduct

Please do check our [Code of Conduct](CODE_OF_CONDUCT.md) before making contributions.

## Topics

* [Reporting security issues](#reporting-security-issues)
* [Reporting general issues](#reporting-general-issues)
* [Code and doc contribution](#code-and-documentation-contribution)
* [Engage to help anything](#engage-to-help-anything)

## Reporting security issues

We take security issues seriously and discourage anyone to spread security issues.
If you find a security issue in KubeAdmiral, please do not discuss it in public and even do not open a public issue.
Instead we encourage you to send us a private email to <mailto:kubewharf.conduct@bytedance.com> to report the security issue.

## Reporting general issues

Any user is welcome be a contributor. If you have any feedback for the project, feel free to open an issue. 

Since KubeAdmiral development will be collaborated in a distributed manner, we appreciate **WELL-WRITTEN**, **DETAILED**, **EXPLICIT** issue reports.
To make communication more efficient, we suggest everyone to search if your issue is an existing one before filing a new issue.
If you find it to be existing, please append your details in the issue comments.

There are lot of cases for which you could open an issue:

* Bug report
* Feature request
* Performance issues
* Feature proposal
* Feature design
* Help wanted
* Doc incomplete
* Test improvement
* Any questions about the project, and so on

Please be reminded that when filing a new issue, do remove the sensitive data from your post.
Sensitive data could be password, secret key, network locations, private business data and so on.

## Code and documentation contribution

Any action that may make KubeAdmiral better is encouraged. The action can be realized via a PR (pull request).

* If you find a typo, try to fix it!
* If you find a bug, try to fix it!
* If you find some redundant codes, try to remove them!
* If you find some test cases missing, try to add them!
* If you could enhance a feature, please **DO NOT** hesitate!
* If you find code implicit, try to add comments to make it clear!
* If you find tech debts, try to refactor them!
* If you find document incorrect, please fix that!

It is impossible to list them completely, we are looking forward to your pull requests.
Before submitting a PR, we suggest you could take a look at the PR rules here.

* [Workspace Preparation](#workspace-preparation)
* [Branch Definition](#branch-definition)
* [Commit Rules](#commit-rules)
* [PR Description](#pr-description)
* [Code style](#code-style)
* [Testing](#testing)

### Workspace Preparation

We assume you have a GitHub ID already, then you could finish the preparation in the following steps:

1. **FORK** KubeAdmiral to your repository. To make this work, you just need to click the button `Create fork` on [kubeadmiral fork page](https://github.com/kubewharf/kubeadmiral/fork). Then you will end up with your repository in `https://github.com/<username>/kubeadmiral`, in which `username` is your GitHub ID.
1. **CLONE** your own repository to develop locally. Use `git clone https://github.com/<username>/kubeadmiral.git` to clone repository to your local machine. Then you can create new branches to finish the change you wish to make.
1. **Set Remote** upstream to be kubeadmiral using the following two commands:

```
git remote add upstream https://github.com/kubeadmiralio/kubeadmiral.git
git remote set-url --push upstream no-pushing
```

With this remote setting, you can check your git remote configuration like this:

```
$ git remote -v
origin     https://github.com/<username>/kubeadmiral.git (fetch)
origin     https://github.com/<username>/kubeadmiral.git (push)
upstream   https://github.com/kubewharf/kubeadmiral.git (fetch)
upstream   no-pushing (push)
```

With above, we can easily synchronize local branches with upstream branches.

You can test your local changes to KubeAdmiral using following commands:

```bash
make kind # create a federation of host and members using kind
make 
```

### Branch Definition

Right now we assume every contribution via pull request is for the `main` branch in KubeAdmiral.

### Commit Rules

When submitting pull requests, please use [conventional commits](https://www.conventionalcommits.org/en/v1.0.0/).
This is to help us generate changelogs effectively and triage commits.

Please also keep commits small and avoid involving unrelated changes into the same commit.
This helps reviewing pull requests more quickly and avoids wasting time resolving git conflicts.

### PR Description

PR is the only way to make change to KubeAdmiral project. To help reviewers, we actually encourage contributors to make PR description as detailed as possible.

### Code style

Please refer to our [code style](./docs/code-style.md) guide before submitting any code.

### Testing

We are currently in the midst of increasing our coverage for both unit tests and end-to-end tests. You may refer to the [e2e documentation](./docs/e2e-tests.md) to find out how to write and run end-to-end tests for KubeAdmiral.

## Engage to help anything

GitHub is the primary place for KubeAdmiral contributors to collaborate. Although contributions via PR is an explicit way to help, we still call for any other types of helps.

* Reply to other's issues if you could;
* Help solve other user's problems;
* Help review other's PR design;
* Help review other's codes in PR;
* Discuss about KubeAdmiral to make things clearer;
* Advocate KubeAdmiral technology beyond GitHub;
* Write blogs on KubeAdmiral, and so on.

In a word, **ANY HELP CAN BE A CONTRIBUTION.**

