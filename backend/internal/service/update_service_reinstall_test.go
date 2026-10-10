//go:build unit

package service

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUpdateServiceRollbackListIncludesCurrentAndThreeOlderVersions(t *testing.T) {
	svc := newRollbackTestService("0.2.15", []*GitHubRelease{
		{TagName: "v0.2.16"},
		{TagName: "v0.2.13"},
		{TagName: "v0.2.15"},
		{TagName: "v0.2.14"},
		{TagName: "v0.2.12"},
		{TagName: "v0.2.11"},
	})
	versions, err := svc.ListRollbackVersions(context.Background())
	require.NoError(t, err)
	require.Len(t, versions, 4)
	for i, version := range []string{"0.2.15", "0.2.14", "0.2.13", "0.2.12"} {
		require.Equal(t, version, versions[i].Version)
	}
	require.Equal(t, "iceyarmu/sub2api", svc.githubClient.(*updateServiceGitHubClientStub).recentRepo)
}

type rollbackDownloadClientStub struct {
	updateServiceGitHubClientStub
	downloadURLs []string
	checksumURLs []string
}

func (s *rollbackDownloadClientStub) DownloadFile(_ context.Context, url, dest string, _ int64) error {
	s.downloadURLs = append(s.downloadURLs, url)
	return os.WriteFile(dest, []byte("archive fixture"), 0600)
}

func (s *rollbackDownloadClientStub) FetchChecksumFile(_ context.Context, url string) ([]byte, error) {
	s.checksumURLs = append(s.checksumURLs, url)
	// Stop before replacing the test executable, after exercising both download paths.
	return nil, errors.New("checksum fixture stop")
}

func TestUpdateServiceRollbackDownloadsFreshAssetsFromFork(t *testing.T) {
	for _, target := range []string{"0.2.15", "v0.2.15", "0.2.14"} {
		t.Run(target, func(t *testing.T) {
			client := &rollbackDownloadClientStub{}
			svc := NewUpdateService(&updateServiceCacheStub{}, client, "0.2.15", "release")
			archive := "sub2api_" + svc.getArchiveName() + ".tar.gz"
			for _, tag := range []string{"v0.2.15", "v0.2.14"} {
				client.recentReleases = append(client.recentReleases, &GitHubRelease{
					TagName: tag,
					Assets: []GitHubAsset{
						{Name: archive, BrowserDownloadURL: "https://github.com/Wei-Shaw/sub2api/releases/download/" + tag + "/" + archive},
						{Name: "checksums.txt", BrowserDownloadURL: "https://github.com/Wei-Shaw/sub2api/releases/download/" + tag + "/checksums.txt"},
					},
				})
			}
			// Retrying a same-version install must download again, not use update cache.
			for range 2 {
				err := svc.RollbackToVersion(context.Background(), target)
				require.ErrorContains(t, err, "checksum fixture stop")
			}
			tag := target
			if tag[0] != 'v' {
				tag = "v" + tag
			}
			base := "https://github.com/iceyarmu/sub2api/releases/download/" + tag + "/"
			require.Equal(t, []string{base + archive, base + archive}, client.downloadURLs)
			require.Equal(t, []string{base + "checksums.txt", base + "checksums.txt"}, client.checksumURLs)
			require.Equal(t, "iceyarmu/sub2api", client.recentRepo)
		})
	}
}
