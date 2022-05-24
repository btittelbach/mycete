package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path"
	"strings"
	"time"
	"sort"
	"errors"

	"github.com/btittelbach/cachetable"
	"github.com/matrix-org/gomatrix"
)

/// unfortunately, since neither go-twitter, anaconda or go-mastodon implement an io.Reader interface we have to use actual temporary files

const uploadfile_type_media_ = "media" //doesn't really need to contain
const uploadfile_type_desc_ = "txt"

// check a size againt known limits, depending on which social media services are enabled
func checkImageBytesizeLimit(size int64) error {
	var max_image_bytes int64 = 10 * 1024 * 1024
	if c["server"]["twitter"] == "true" && size > imgbytes_limit_twitter_ {
		return fmt.Errorf("Image too large for Twitter. Please shrink to below %d bytes", imgbytes_limit_twitter_)
	}
	if c["server"]["mastodon"] == "true" && size > imgbytes_limit_mastodon_ {
		return fmt.Errorf("Image too large for Mastodon. Please shrink to below %d bytes", imgbytes_limit_mastodon_)
	}
	if size > max_image_bytes {
		return fmt.Errorf("Image is too large. Please shrink to below %d bytes", max_image_bytes)
	}
	return nil
}

// read a file into memory and return it's contents as base64 encoded string
func readFileIntoBase64(filepath string) (string, error) {
	contents, err := ioutil.ReadFile(filepath)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(contents), nil
}

// return number of elements in a directory.
// at most we read (feed2matrx_image_count_limit_ + 1) dirctory entries
// and my thus return less than the true amount of files in the directory
// but this is ok, since all calling functions only need to know
// if the number exceeds (feed2matrx_image_count_limit_).
// This way we can safely limit Readdir.
func osGetLimitedNumElementsInDir(directory string) (int, error) {
	f, err := os.Open(directory)
	if err != nil {
		return 0, err
	}
	fileInfo, err := f.Readdir(feed2matrx_image_count_limit_ + 1)
	f.Close()
	if err != nil && err != io.EOF {
		return 0, err
	}
	return len(fileInfo), nil
}

// Return list of media files, currently uploaded and prepapred for posting, for a matrix-user
// returns at most (feed2matrx_image_count_limit_) list entries
func getUserFileList(nick string) ([]string, error) {
	usermediadir := path.Join(hashNickToUserDir(nick), uploadfile_type_media_)
	f, err := os.Open(usermediadir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	names, err := f.Readdirnames(feed2matrx_image_count_limit_)
	f.Close()
	if err != nil && err != io.EOF {
		return nil, err
	}
	fullnames := make([]string, len(names))
	for idx, filename := range names {
		fullnames[idx] = path.Join(usermediadir, filename)
	}
	return fullnames, nil
}

// takes an array of file paths and a maximum file age
// returns two arrays of files, separated by wether their file-age is below or above the given argument
func filterFilelistByFileAge(full_filepaths []string, timeout time.Duration) (still_within_timeout_filepaths []string, older_than_timeout_filepaths []string, err error) {
	now := time.Now()
	still_within_timeout_filepaths = make([]string, 0, len(full_filepaths))
	older_than_timeout_filepaths = make([]string, 0, len(full_filepaths))
	for _, fpath := range full_filepaths {
		var fstat os.FileInfo
		fstat, err = os.Stat(fpath)
		if err != nil {
			return
		}
		if now.Sub(fstat.ModTime()) > timeout {
			older_than_timeout_filepaths = append(older_than_timeout_filepaths, fpath)
		} else {
			still_within_timeout_filepaths = append(still_within_timeout_filepaths, fpath)
		}
	}
	return
}

// takes an array of file paths and returns them sorted by age, youngest first
func getUserFilelistSortedByMtime(nick, filetype string) (sorted_filepaths []string, err error) {
	usermediadir := path.Join(hashNickToUserDir(nick), filetype)
	var files []os.FileInfo
	files, err = ioutil.ReadDir(usermediadir)
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i,j int) bool{
	    return files[i].ModTime().After(files[j].ModTime())
	})

	for _, onepath := range files {
		sorted_filepaths = append(sorted_filepaths, path.Join(usermediadir, onepath.Name()))
	}

	return sorted_filepaths, nil
}


func saveMatrixFile(cli *gomatrix.Client, nick, eventid, matrixurl string) error {
	if !strings.Contains(matrixurl, "mxc://") {
		return fmt.Errorf("image url not a matrix content mxc://..  uri")
	}
	matrixmediaurlpart := strings.Split(matrixurl, "mxc://")[1]
	filesdir, imgfilepath := hashNickAndTypeAndEventIdToPath(nick, uploadfile_type_media_, eventid)
	os.MkdirAll(filesdir, 0700)
	imgtmpfilepath := imgfilepath + ".tmp"

	numfiles, err := osGetLimitedNumElementsInDir(filesdir)
	if err != nil {
		return err
	}
	/// limit number of files per user
	if numfiles >= feed2matrx_image_count_limit_ {
		return fmt.Errorf("Too many files stored. %d is the limit.", feed2matrx_image_count_limit_)
	}

	/// Create the file (implies truncate)
	fh, err := os.OpenFile(imgtmpfilepath, os.O_WRONLY|os.O_CREATE, 0400)
	if err != nil {
		return err
	}

	defer fh.Close()

	/// Download image
	mcxurl := cli.BuildBaseURL("/_matrix/media/r0/download/", matrixmediaurlpart)
	resp, err := http.Get(mcxurl)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Check Filesize (again)
	if err = checkImageBytesizeLimit(resp.ContentLength); err != nil {
		os.Remove(imgtmpfilepath) //remove before close will work on unix/bsd. Not sure about windows, but meh.
		return err
	}

	// Write the body to file
	var bytes_written int64
	bytes_written, err = io.Copy(fh, resp.Body)
	if err != nil {
		return err
	}

	// Check Filesize (again)
	if err = checkImageBytesizeLimit(bytes_written); err != nil {
		if resp.ContentLength > 0 {
			log.Printf("Content-Length lied to us != bytes_written: %d != %d", resp.ContentLength, bytes_written)
		}
		os.Remove(imgtmpfilepath) //remove before close will work on unix/bsd. Not sure about windows, but meh.
		return err
	}

	//NOTE: FIXME: close has not been called, at atomic rename time, file may not have been fully written. This is mostly fine, since fh can still be written too since only filename changed, but if we were to interact with other processes, which we don't, this would be a race condition
	os.Rename(imgtmpfilepath, imgfilepath)
	return nil
}

func saveMediaFileDescription(nick, eventid_of_related_img, description string) error {
	_, imgfilepath := hashNickAndTypeAndEventIdToPath(nick, uploadfile_type_media_, eventid_of_related_img)

	if _, err := os.Stat(imgfilepath); errors.Is(err, os.ErrNotExist) {
	  return fmt.Errorf("corresponding media file does not exist")
	}

	filesdir, descfilepath := hashNickAndTypeAndEventIdToPath(nick, uploadfile_type_desc_, eventid_of_related_img)
	os.MkdirAll(filesdir, 0700)
	desctmpfilepath := descfilepath + ".tmp"

	numfiles, err := osGetLimitedNumElementsInDir(filesdir)
	if err != nil {
		return err
	}
	/// limit number of files per user
	if numfiles >= feed2matrx_image_count_limit_ {
		return fmt.Errorf("Too many files stored. %d is the limit.", feed2matrx_image_count_limit_)
	}

	/// Create the file (implies truncate)
	fh, err := os.OpenFile(desctmpfilepath, os.O_WRONLY|os.O_CREATE, 0400)
	if err != nil {
		return err
	}

	/// Save description
	fh.Write([]byte(description))
	fh.Close()

	os.Rename(desctmpfilepath, descfilepath)
	return nil
}

func getDescriptionFilenameOfMediaFilename(imgfilepath string) (string, error) {
	usermediadir, filename := path.Split(imgfilepath)
	userdir, mediatype := path.Split(usermediadir)
	if mediatype != uploadfile_type_media_ {
		return "", fmt.Errorf("unknown imgfilepath given")
	}
	descfile := path.Join(userdir, uploadfile_type_desc_, filename)
	return descfile, nil
}

func readDescriptionOfMediaFile(imgfilepath string) (string, error) {
	descfile, err := getDescriptionFilenameOfMediaFilename(imgfilepath)
	if err != nil {
		return "", err
	}
	desc, err := os.ReadFile(descfile)
	if err != nil {
		return "", err
	} else {
		return string(desc), nil
	}
}

func addMediaFileDescriptionToLastMediaUpload(nick, description string) error {
	sorted_media, err := getUserFilelistSortedByMtime(nick, uploadfile_type_media_)
	if err != nil {
		log.Println("Error getUserFilelistSortedByMtime:", err)
	}
	if len(sorted_media) == 0 {
		return fmt.Errorf("no media files have been uploaded")
	}
	descfile, err := getDescriptionFilenameOfMediaFilename(sorted_media[0])

	/// Create the file (implies truncate)
	fh, err := os.OpenFile(descfile, os.O_WRONLY|os.O_CREATE, 0400)
	if err != nil {
		return err
	}

	/// Save description
	fh.Write([]byte(description))
	fh.Close()
	return nil
}

func rmFile(nick, eventid string) error {
	// log.Println("removing file for", nick)
	_, fpath := hashNickAndTypeAndEventIdToPath(nick, uploadfile_type_desc_, eventid)
	os.Remove(fpath)
	_, fpath = hashNickAndTypeAndEventIdToPath(nick, uploadfile_type_media_, eventid)
	return os.Remove(fpath)
}

func rmAllUserFiles(nick string) error {
	return os.RemoveAll(hashNickToUserDir(nick))
}

/// return hex(sha256()) of string
/// used so malicous user can't use malicous filename that is defined by nick. (and hash collision or guessing not so big a threat here.)
func hashNickToUserDir(matrixnick string) string {
	shasum := make([]byte, sha256.Size)
	shasum32 := sha256.Sum256([]byte(matrixnick))
	copy(shasum[0:sha256.Size], shasum32[0:sha256.Size])
	return path.Join(temp_image_files_dir_, hex.EncodeToString(shasum))
}

func hashNickAndTypeAndEventIdToPath(matrixnick, filetype, eventid string) (string, string) {
	shasum := make([]byte, sha256.Size)
	shasum32 := sha256.Sum256([]byte(eventid))
	copy(shasum[0:sha256.Size], shasum32[0:sha256.Size])
	userdir := hashNickToUserDir(matrixnick)
	filetypedir := path.Join(userdir, filetype)
	return filetypedir, path.Join(filetypedir, hex.EncodeToString(shasum))
}

type MxUploadedImageInfo struct {
	mxcurl        string
	mimetype      string
	contentlength int64
	err           error
}

type MxContentUrlFuture struct {
	imgurl          string
	future_mxcurl_c chan MxUploadedImageInfo
}

func matrixUploadLink(mxcli *gomatrix.Client, url string) (*gomatrix.RespMediaUpload, string, int64, error) {
	response, err := mxcli.Client.Get(url)
	if response != nil {
		defer response.Body.Close()
	}
	if err != nil {
		return nil, "", 0, err
	}
	mimetype := response.Header.Get("Content-Type")
	clength := response.ContentLength
	if clength > feed2matrx_image_bytes_limit_ {
		return nil, "", 0, fmt.Errorf("media's size exceeds imagebyteslimit: %d > %d", clength, feed2matrx_image_bytes_limit_)
	}
	rmu, err := mxcli.UploadToContentRepo(response.Body, mimetype, clength)
	return rmu, mimetype, clength, err
}

func taskUploadImageLinksToMatrix(mxcli *gomatrix.Client) chan<- MxContentUrlFuture {
	futures_chan := make(chan MxContentUrlFuture, 42)
	go func() {
		mx_link_store, err := cachetable.NewCacheTable(70, 9, false)
		if err != nil {
			panic(err)
		}
		for future := range futures_chan {
			resp := MxUploadedImageInfo{}
			if saveddata, inmap := mx_link_store.Get(future.imgurl); inmap {
				resp = saveddata.Value.(MxUploadedImageInfo)
			} else { // else upload it
				if resp_media_up, mimetype, clength, err := matrixUploadLink(mxcli, future.imgurl); err == nil {
					resp.mxcurl = resp_media_up.ContentURI
					resp.contentlength = clength
					resp.mimetype = mimetype
					resp.err = err
					mx_link_store.Set(future.imgurl, resp)
				} else {
					resp.err = err
					log.Printf("uploadImageLinksToMatrix Error: url: %s, error: %s", future.imgurl, err.Error())
				}
			}
			//return something to future in every case
			if future.future_mxcurl_c != nil {
				select {
				case future.future_mxcurl_c <- resp:
				default:
				}
			}
		}
	}()
	return futures_chan
}
