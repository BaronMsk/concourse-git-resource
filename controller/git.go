package controller

import (
	"github.com/libgit2/git2go"
	log "github.com/sirupsen/logrus"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
)

type Config struct {
	Input *Payload
	Path string
}

type Payload struct {
	Source  Source `json:"source"`
	Version Ref    `json:"version"`
}

type Ref struct {
	Ref string `json:"ref"`
}

type Source struct {
	Url        string `json:"uri"`
	Branch     string `json:"branch"`
	TagFilter  string `json:"tag_filter"`
	PathSearch []string `json:"paths"`
	PrivateKey string `json:"private_key"`
}

type MetadataJson struct {
	Version  Ref                 `json:"version"`
	Metadata []map[string]string `json:"metadata"`
}

type Tag struct {
	Name   string
	Commit string
	When   int64
}

type RefResult []map[string]string

var sshKeyPath = "/root/.ssh/"

func Init(config Config) {
	if !exists(sshKeyPath) {
		os.MkdirAll(sshKeyPath, 0755)
		ioutil.WriteFile(sshKeyPath+"id_rsa", []byte(config.Input.Source.PrivateKey), 0600)
		createSshPubKey()
	}
	if config.Input.Source.Branch == "" {
		config.Input.Source.Branch = "master"
	}
	if exists(config.Path + "/.git") {
		fetchRepo(config.Path)
	} else {
		cloneRepo(config.Input.Source.Url, config.Input.Source.Branch, config.Path)
	}
}

func Check(config Config) RefResult {
	if config.Input.Source.Branch == "" {
		config.Input.Source.Branch  = "master"
	}
	if config.Input.Source.TagFilter != "" {
		return checkTagFilter(config)
	}
	if config.Input.Source.PathSearch != nil {
		return checkPaths(config)
	} else {
		return checkCommit(config)
	}
	return nil
}

func Checkout(config Config) {
	repo, err := git.OpenRepository(config.Path)
	if err != nil {
		log.Fatal(err)
	}
	var oid *git.Oid
	obj, err := repo.References.Lookup("refs/tags/" + config.Input.Version.Ref)
	if obj != nil {
		oid = obj.Target()
	} else {
		oid, _ = git.NewOid(config.Input.Version.Ref)
	}
	repo.SetHeadDetached(oid)
	repo.CheckoutHead(&git.CheckoutOpts{Strategy: git.CheckoutForce})
	defer repo.Free()

}

func checkPaths(config Config) RefResult {
	if config.Input.Version.Ref == "" {
		return checkCommit(config)
	}
	for _, pathSearch := range config.Input.Source.PathSearch{
		for _, pf := range diff(config) {
			if pf == pathSearch {
				return checkCommit(config)
			}
		}
	}
	return nil
}

func checkCommit(config Config) RefResult {
	if config.Input.Version.Ref != "" {
		return lastCommits(config)
	}
	repo, err := git.OpenRepository(config.Path)
	if err != nil {
		log.Fatal(err)
	}
	remoteBranch, err := repo.References.Lookup("refs/remotes/origin/" + config.Input.Source.Branch)
	if err != nil {
		log.Fatal(err)
	}
	commit, err := repo.LookupCommit(remoteBranch.Target())
	if err != nil {
		log.Fatal(err)
	}
	var result RefResult
	c := make(map[string]string)
	c["ref"] = commit.Id().String()
	result = append(result, c)
	defer repo.Free()
	return result
}

func GetMetaData(path string, input Payload) RefResult {
	repo, err := git.OpenRepository(path)
	if err != nil {
		log.Fatal(err)
	}
	var oid *git.Oid
	obj, err := repo.References.Lookup("refs/tags/" + input.Version.Ref)
	if obj != nil {
		oid = obj.Target()
	} else {
		oid, _ = git.NewOid(input.Version.Ref)
	}
	o, err := repo.LookupCommit(oid)
	if err != nil {
		log.Fatal(err)
	}
	var result []map[string]string

	commit := make(map[string]string)
	commit["name"] = "commit"
	commit["value"] = o.Id().String()

	author := make(map[string]string)
	author["name"] = "author"
	author["value"] = o.Committer().Name

	whenCommit := make(map[string]string)
	whenCommit["name"] = "date"
	whenCommit["value"] = o.Committer().When.String()

	branch := make(map[string]string)
	branch["name"] = "branch"
	branch["value"] = input.Source.Branch

	tag := make(map[string]string)
	tag["name"] = "tag"
	tag["value"] = ""

	if obj != nil {
		tag["value"] = input.Version.Ref
	}

	message := make(map[string]string)
	message["name"] = "message"
	message["value"] = o.Message()

	result = append(result, commit, author, whenCommit, branch, tag, message)
	defer repo.Free()
	return result
}

func checkTagFilter(config Config) RefResult {
	list := listTags(config.Path, config.Input.Source.TagFilter)

	if config.Input.Version.Ref != "" {
		return lastTags(list, config)
	}

	if list != nil {
		return lastTag(list)
	}

	return nil
}

func createSshPubKey() {
	cmd := exec.Command("ssh-keygen", "-y", "-f", "/root/.ssh/id_rsa")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		log.Fatal(err)
	}
	go func() {
		defer stdin.Close()
		io.WriteString(stdin, "values written to stdin are passed to cmd's standard input")
	}()
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Fatal(err)
	}
	ioutil.WriteFile(sshKeyPath+"id_rsa.pub", []byte(out), 0644)
}

func credentialsCallback(url string, username string, allowedTypes git.CredType) (git.ErrorCode, *git.Cred) {
	ret, cred := git.NewCredSshKey("git", sshKeyPath+"id_rsa.pub", sshKeyPath+"id_rsa", "")
	return git.ErrorCode(ret), &cred
}

func certificateCheckCallback(cert *git.Certificate, valid bool, hostname string) git.ErrorCode {
	return 0
}

func exists(path string) bool {
	_, err := os.Stat(path)
	if err != nil || os.IsNotExist(err) {
		return false
	}
	return true
}

func cloneRepo(url, branch, path string) {
	cloneOptions := &git.CloneOptions{}
	cloneOptions.CheckoutBranch = branch
	cloneOptions.FetchOptions = &git.FetchOptions{
		RemoteCallbacks: git.RemoteCallbacks{
			CredentialsCallback:      credentialsCallback,
			CertificateCheckCallback: certificateCheckCallback,
		},
	}
	_, err := git.Clone(url, path, cloneOptions)
	if err != nil {
		log.Fatal(err, url)
	}
}

func fetchRepo(path string) {
	FetchOptions := &git.FetchOptions{
		RemoteCallbacks: git.RemoteCallbacks{
			CredentialsCallback:      credentialsCallback,
			CertificateCheckCallback: certificateCheckCallback,
		},
	}
	repo, err := git.OpenRepository(path)
	if err != nil {
		log.Fatal(err)
	}
	remote, err := repo.Remotes.Lookup("origin")
	if err != nil {
		log.Fatal(err)
	}
	err = remote.Fetch(nil, FetchOptions, "")
	if err != nil {
		log.Fatal(err)
	}
}

func tagWhen(repo *git.Repository, oid *git.Oid) int64 {
	obj, err := repo.Lookup(oid)
	if err != nil {
		log.Fatal(err)
	}
	if obj.Type() == git.ObjectTag {
		o, err := repo.LookupTag(oid)
		if err != nil {
			log.Fatal(err)
		}
		return o.Tagger().When.Unix()
	}
	if obj.Type() == git.ObjectCommit {
		o, err := repo.LookupCommit(oid)
		if err != nil {
			log.Fatal(err)
		}
		return o.Committer().When.Unix()
	}
	return 0
}

func lastCommits(config Config) RefResult  {
	repo, err := git.OpenRepository(config.Path)
	if err != nil {
		log.Fatal(err)
	}
	odb, err := repo.Odb()
	if err != nil {
		log.Fatal(err)
	}
	var allCommitList []string
	err = odb.ForEach(func(id *git.Oid) error {
		obj, err := repo.Lookup(id)
		if err != nil {
			log.Fatal(err)
		}
		if obj.Type() == git.ObjectCommit {
			allCommitList = append(allCommitList, obj.Id().String())
		}
		return nil
	})
	var result  RefResult
	for _, c := range allCommitList {
		ref := make(map[string]string)
		ref["ref"] = c
		result = append([]map[string]string{ref}, result...)
		if config.Input.Version.Ref == c {
			break
		}
	}
	return result
}

func listTags(path, tagFilter string) []Tag {
	repo, err := git.OpenRepository(path)
	if err != nil {
		log.Fatal(err)
	}
	var result []Tag
	err = repo.Tags.Foreach(func(name string, id *git.Oid) error {
		t := Tag{Name: name,
		Commit: id.String(),
		When: tagWhen(repo, id)}
		re := regexp.MustCompilePOSIX(tagFilter)
		if re.MatchString(name) {
			result = append(result, t)
		}
		return nil
	})
	defer repo.Free()
	return result
}

func lastTag(listTag []Tag) RefResult {
	sort.Slice(listTag, func(i, j int) bool {
		if listTag[i].When < listTag[j].When {
			return true
		}
		if listTag[i].When > listTag[j].When {
			return false
		}
		return listTag[i].When < listTag[j].When
	})
	lt := listTag[len(listTag)-1]
	lastTag := make(map[string]string)
	lastTag["ref"] = strings.Trim(lt.Name, "refs/tags/")
	var result RefResult
	result = append(result, lastTag)
	return result
}

func lastTags(listTag []Tag, config Config) RefResult {
	sort.Slice(listTag, func(i, j int) bool {
		if listTag[i].When > listTag[j].When {
			return true
		}
		if listTag[i].When < listTag[j].When {
			return false
		}
		return listTag[i].When > listTag[j].When
	})
	var result RefResult
	for _, t := range listTag {
		lt := strings.Trim(t.Name, "refs/tags/")
		ref := make(map[string]string)
		ref["ref"] = lt
		result = append([]map[string]string{ref}, result...)
		if config.Input.Version.Ref == lt {
			break
		}
	}
	return result
}

func lookupCommit(repo *git.Repository, ref string) *git.Tree {
	oid, err := git.NewOid(ref)
	if err != nil {
		log.Fatal(err)
	}
	obj, err := repo.LookupCommit(oid)
	if err != nil {
		log.Fatal(err)
	}
	tree, err := repo.LookupTree(obj.TreeId())
	if err != nil {
		log.Fatal(err)
	}
	defer tree.Free()
	return tree
}

func diff(config Config) []string {
	repo, err := git.OpenRepository(config.Path)
	if err != nil {
		log.Fatal(err)
	}
	localBranch, err := repo.LookupBranch("origin/"+config.Input.Source.Branch, git.BranchRemote)
	if err != nil {
		log.Fatal(err)
	}
	commit, err := repo.LookupCommit(localBranch.Target())
	if err != nil {
		log.Fatal(err)
	}
	originalTree, err := repo.LookupTree(commit.TreeId())
	if err != nil {
		log.Fatal(err)
	}
	refTree := lookupCommit(repo, config.Input.Version.Ref)
	callbackInvoked := false
	opts := git.DiffOptions{
		NotifyCallback: func(diffSoFar *git.Diff, delta git.DiffDelta, matchedPathSpec string) error {
			callbackInvoked = true
			return nil
		},
	}
	diff, err := repo.DiffTreeToTree(originalTree, refTree, &opts)
	if err != nil {
		log.Fatal(err)
	}
	files := make([]string, 0)
	hunks := make([]git.DiffHunk, 0)
	lines := make([]git.DiffLine, 0)
	patches := make([]string, 0)
	err = diff.ForEach(func(file git.DiffDelta, progress float64) (git.DiffForEachHunkCallback, error) {
		patch, err := diff.Patch(len(patches))
		if err != nil {
			return nil, err
		}
		defer patch.Free()
		patchStr, err := patch.String()
		if err != nil {
			return nil, err
		}
		patches = append(patches, patchStr)
		files = append(files, file.OldFile.Path)
		return func(hunk git.DiffHunk) (git.DiffForEachLineCallback, error) {
			hunks = append(hunks, hunk)
			return func(line git.DiffLine) error {
				lines = append(lines, line)
				return nil
			}, nil
		}, nil
	}, git.DiffDetailLines)
	defer repo.Free()
	return files
}
